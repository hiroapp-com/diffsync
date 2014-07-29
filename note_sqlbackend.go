package diffsync

import (
	"fmt"
	"log"
	"strconv"
	"time"

	"database/sql"

	"github.com/hiro/hync/comm"
	DMP "github.com/sergi/go-diff/diffmatchpatch"
)

var (
	_ = log.Print
)

type NoteSQLBackend struct {
	db *sql.DB
}

func NewNoteSQLBackend(db *sql.DB) NoteSQLBackend {
	return NoteSQLBackend{db}
}

func (backend NoteSQLBackend) Get(key string) (ResourceValue, error) {
	note := NewNote("")
	var txt, createdBy string
	var createdAt time.Time
	err := backend.db.QueryRow("SELECT title, txt, sharing_token, created_at, created_by FROM notes WHERE nid = ?", key).Scan(&note.Title, &txt, &note.SharingToken, &createdAt, &createdBy)
	switch {
	case err == sql.ErrNoRows:
		return nil, NoExistError{key}
	case err != nil:
		return nil, err
	}
	note.Text = TextValue(txt)
	note.CreatedAt = UnixTime(createdAt)
	note.CreatedBy = User{UID: createdBy}
	peers := PeerList{}
	rows, err := backend.db.Query("SELECT nr.uid, users.email, nr.cursor_pos, nr.last_seen, nr.last_edit, nr.role FROM noterefs as nr LEFT OUTER JOIN users ON (users.uid = nr.uid and users.email_status in ('verified', 'invited')) WHERE nid = ?", key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		peer := Peer{User: User{}}
		var email sql.NullString
		if err := rows.Scan(&peer.User.UID, &email, &peer.CursorPosition, &peer.LastSeen, &peer.LastEdit, &peer.Role); err != nil {
			return nil, err
		}
		if email.Valid {
			peer.User.Email = email.String
		}
		peers = append(peers, peer)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	note.Peers = peers
	return note, nil
}

func (backend NoteSQLBackend) Patch(nid string, patch Patch, store *Store, ctx context) error {
	log.Printf("note-backend: received patch for note[%s]: %v", nid, patch)
	switch patch.Op {
	case "text":
		// patch.Path empty
		// patch.Value contains text-patches
		// patch.OldValue empty
		err := backend.patchText(nid, patch.Value.([]DMP.Patch))
		if err != nil {
			return fmt.Errorf("notesqlbackend: note(%s) could not be patched. patch: `%v`, err: `%s`", nid, patch.Value, err)
		}
		if err = backend.pokeTimers(nid, true, ctx); err != nil {
			log.Printf("notesqlbackend: note(%s) couldnot poke edit-timers for uid %s. err: %s", nid, ctx.uid, err)
		}
	case "title":
		// patch.Path empty
		// patch.Value contains new Title
		// patch.OldValue contains old Title, for reference
		res, err := backend.db.Exec("UPDATE notes SET title = ? WHERE nid = ? and title = ?", patch.Value.(string), nid, patch.OldValue.(string))
		if err != nil {
			return fmt.Errorf("notesqlbackend: note(%s) title could not be set. old: `%s` new `%s`", nid, patch.OldValue.(string), patch.Value.(string))
		}
		numChanges, _ := res.RowsAffected()
		if err = backend.pokeTimers(nid, numChanges > 0, ctx); err != nil {
			log.Printf("notesqlbackend: note(%s) couldnot poke edit-timers for uid %s. err: %s", nid, ctx.uid, err)
		}
	case "invite-user":
		// patch.Path emtpy
		// patch.Value contains User object (maybe without UID)
		// patch.OldValue empty
		// check if current user is not anon
		//profile := Resource{Kind: "profile", ID: ctx.uid}
		//if err := store.Load(&profile); err != nil {
		//	return err
		//}
		//if profile.Value.(Profile).User.Tier < 1 {
		//	// anon users are not allowed to invite. the ui should
		//	// never attempt to do this, thus we'll safely ignore the patch
		//	return nil
		//}
		userRef := patch.Value.(User)
		if userRef.Email != "" {
			userRef.Name = userRef.Email
			userRef.EmailStatus = "invited"
		} else if userRef.Phone != "" {
			userRef.Name = userRef.Phone
			userRef.PhoneStatus = "invited"
		}
		userRef.Tier = -1
		user, _, err := getOrCreateUser(userRef, backend.db)
		if err != nil {
			return err
		}
		// fire and forgeeeeet
		backend.db.Exec("INSERT INTO contacts (uid, contact_uid ) VALUES(?, ?)", ctx.uid, user.UID)
		backend.db.Exec("INSERT INTO contacts (uid, contact_uid ) VALUES(?, ?)", user.UID, ctx.uid)
		//TODO(flo) add contact for every other user what accesses given note OR calculate contacts
		//			from contacts and noterefs in profilebackend.get?
		res, err := backend.db.Exec("INSERT INTO noterefs (nid, uid, role) VALUES(?, ?, 'invited')", nid, user.UID)
		if err != nil {
			return fmt.Errorf("could not create note-ref for invitee: nid: %s, uid: %s", nid, user.UID)
		}
		if affected, _ := res.RowsAffected(); affected > 0 {
			store.SendInvitation(user, nid)
		}
		// taint profile so its and it's contact's contact lists get updated
		store.NotifyTaint("profile", ctx.uid, ctx)
		// reset so SID gets shadow
		store.NotifyReset("note", nid, ctx) // TODO address SID directly
		// taint to all others get the peers update
		store.NotifyTaint("note", nid, ctx)
	case "set-cursor":
		// patch.Path contains UID of peer whose cursor to set
		// patch.Value contains int64 with new cursor position
		// patch.OldValue contains int64 value of prior known value, usable for CAS
		if patch.Path != ctx.uid {
			return fmt.Errorf("notesqlbackend: cannot set cursor for other user than context user. ctx.uid:%s peer-uid: %s", ctx.uid, patch.Path)
		}
		_, err := backend.db.Exec("UPDATE noterefs SET cursor_pos = ? WHERE nid = ? AND uid = ? AND cursor_pos = ?", patch.Value.(int64), nid, patch.Path, patch.OldValue.(int64))
		if err != nil {
			return err
		}
	case "rem-peer":
		// patch.Path contains UID of peer to remove
		// patch.Value empty
		// patch.OldValue empty
		_, err := backend.db.Exec("DELETE FROM noterefs WHERE nid = ? AND uid = ?", nid, patch.Path)
		if err != nil {
			return err
		}
	case "set-seen":
		// patch.Path contains UID of peer who has seen stuff
		// patch.Value empty
		// patch.OldValue empty
		if ctx.uid != patch.Path {
			return fmt.Errorf("cannot set seen for user other than context user. %s != %s ", ctx.uid, patch.Path)
		}
		if err := backend.pokeTimers(nid, true, ctx); err != nil {
			log.Printf("notesqlbackend: note(%s) couldnot poke edit-timers for uid %s. err: %s", nid, ctx.uid, err)
		}
	}
	return nil
}

func (backend NoteSQLBackend) CreateEmpty(ctx context) (string, error) {
	note := NewNote("")
	nid := generateNID()
	token, hashed := generateToken()
	_, err := backend.db.Exec("INSERT INTO notes (nid, title, txt, sharing_token, created_by) VALUES (?, ?, ?, ?, ?)", nid, note.Title, string(note.Text), token, ctx.uid)
	if err != nil {
		return "", err
	}
	// cleanup, fire and forget
	backend.db.Exec("DELETE FROM tokens WHERE uid = '' and nid = ? and kind = 'share'", nid)
	// create token
	if _, err := backend.db.Exec("INSERT INTO tokens (token, kind, uid, nid) VALUES (?, 'share-url', '', ?)", hashed, nid); err != nil {
		return "", nil
	}
	// already create a noteref (i.e. add to user's folio) if uid provided in context
	if ctx.uid != "" {
		backend.db.Exec("INSERT INTO noterefs (uid, nid, status, role) VALUES (?, ?, 'active', 'owner')", ctx.uid, nid)
	}
	return nid, nil
}

func (backend NoteSQLBackend) patchText(id string, patch []DMP.Patch) error {
	txn, err := backend.db.Begin()
	if err != nil {
		return err
	}
	var original string
	switch err := txn.QueryRow("SELECT txt FROM notes WHERE nid = ?", id).Scan(&original); {
	case err == sql.ErrNoRows:
		txn.Rollback()
		return NoExistError{id}
	case err != nil:
		txn.Rollback()
		return err
	}
	patched, _ := dmp.PatchApply(patch, original)
	if _, err := txn.Exec("UPDATE notes SET txt = ? WHERE nid = ?", patched, id); err != nil {
		txn.Rollback()
		return err
	}
	txn.Commit()
	return nil
}

func (backend NoteSQLBackend) pokeTimers(id string, edited bool, ctx context) (err error) {
	if edited {
		_, err = backend.db.Exec("UPDATE noterefs SET last_seen = datetime('now'), last_edit = datetime('now') WHERE nid = ? AND uid = ?", id, ctx.uid)
	} else {
		_, err = backend.db.Exec("UPDATE noterefs SET last_seen = datetime('now') WHERE nid = ? AND uid = ?", id, ctx.uid)
	}
	return
}

func generateNID() string {
	return randomString(10)
}
