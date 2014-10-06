package diffsync

import (
	"fmt"
	"log"
	"strconv"

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
	var txt string
	err := backend.db.QueryRow("SELECT title, txt, sharing_token FROM notes WHERE nid = ?", key).Scan(&note.Title, &txt, &note.SharingToken)
	switch {
	case err == sql.ErrNoRows:
		return nil, NoExistError{key}
	case err != nil:
		return nil, err
	}
	note.Text = TextValue(txt)
	peers := PeerList{}
	rows, err := backend.db.Query(`SELECT nr.uid, 
										  u1.tier,
										  u1.email, 
										  u1.email_status,
										  u1.phone, 
										  u1.phone_status,
										  nr.cursor_pos, 
										  nr.last_seen, 
										  nr.last_edit, 
										  nr.role 
								    FROM noterefs as nr 
									  LEFT JOIN users as u1
									    ON u1.uid = nr.uid
									WHERE nid = ? 
									  AND tier > -2`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		peer := Peer{User: User{}}
		var email, emailStatus, phone, phoneStatus sql.NullString
		if err := rows.Scan(&peer.User.UID, &peer.User.Tier, &email, &emailStatus, &phone, &phoneStatus, &peer.CursorPosition, &peer.LastSeen, &peer.LastEdit, &peer.Role); err != nil {
			return nil, err
		}
		if emailStatus.Valid && (emailStatus.String == "verified" || emailStatus.String == "invited") {
			peer.User.Email = email.String
		}
		if phoneStatus.Valid && (phoneStatus.String == "verified" || phoneStatus.String == "invited") {
			peer.User.Phone = phone.String
		}
		peers = append(peers, peer)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	note.Peers = peers
	return note, nil
}

func (backend NoteSQLBackend) Patch(nid string, patch Patch, result *SyncResult, ctx Context) error {
	switch patch.Op {
	case "text":
		// patch.Path empty
		// patch.Value contains text-patches
		// patch.OldValue empty
		err := backend.patchText(nid, patch.Value.([]DMP.Patch), result)
		if err != nil {
			return fmt.Errorf("notesqlbackend: note(%s) could not be patched. patch: `%v`, err: `%s`", nid, patch.Value, err)
		}
		if err = backend.pokeTimers(nid, true, ctx); err != nil {
			log.Printf("notesqlbackend: note(%s) couldnot poke edit-timers for uid %s. err: %s", nid, ctx.uid, err)
		}
		result.Tainted(Resource{Kind: "note", ID: nid})
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
		result.Tainted(Resource{Kind: "note", ID: nid})
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
		ref := patch.Value.(User)
		var u *User
		var err error
		if len(ref.UID) == 8 {
			u, err = findUserByUID(backend.db, ref.UID)
			if err != nil {
				return err
			} else if u == nil {
				// user not found
				return fmt.Errorf("cannot invite user to note: user not found")
			}
		}

		if ref.Email != "" {
			u, err = findUserByEmail(backend.db, ref.Email)
			if err != nil {
				return err
			}
			if u == nil {
				// email provided but not found in DB, create invited user
				u.Email = ref.Email
				if err = createInvitedUser(backend.db, u); err != nil {
					return err
				}
			}
		} else if ref.Phone != "" {
			u, err = findUserByPhone(backend.db, ref.Phone)
			if err != nil {
				return err
			}
			if u == nil {
				// email provided but not found in DB, create invited user
				u.Phone = ref.Phone
				if err = createInvitedUser(backend.db, u); err != nil {
					return err
				}
			}
		}
		if u == nil {
			panic("NULLUSER WTF?!")
		}
		res, err := backend.db.Exec("INSERT INTO noterefs (nid, uid, role, status) VALUES(?, ?, 'peer', 'active')", nid, u.UID)
		if err != nil {
			return fmt.Errorf("could not create note-ref for invitee: nid: %s, uid: %s", nid, u.UID)
		}
		if affected, _ := res.RowsAffected(); affected > 0 {
			go backend.sendInvite(*u, nid, ctx)
			if err = ctx.Router.Handle(Event{UID: u.UID, Name: "res-add", Res: Resource{Kind: "note", ID: nid}, ctx: ctx}); err != nil {
				return err
			}
			if err = ctx.Router.Handle(Event{UID: u.UID, Name: "res-sync", Res: Resource{Kind: "folio", ID: u.UID}, ctx: ctx}); err != nil {
				return err
			}
			result.Tainted(Resource{Kind: "note", ID: nid})
			// lastly create contact with provided infos
			return createContact(ctx.uid, u.UID, ref.Name, ref.Email, ref.Phone, backend.db, ctx)
		}
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
		result.Tainted(Resource{Kind: "note", ID: nid})
	case "rem-peer":
		// patch.Path contains UID of peer to remove
		// patch.Value empty
		// patch.OldValue empty
		_, err := backend.db.Exec("DELETE FROM noterefs WHERE nid = ? AND uid = ?", nid, patch.Path)
		if err != nil {
			return err
		}
		ctx.Router.Handle(Event{UID: patch.Path, Name: "res-remove", Res: Resource{Kind: "note", ID: nid}, ctx: ctx})
		result.Tainted(Resource{Kind: "folio", ID: patch.Path})
		result.Tainted(Resource{Kind: "note", ID: nid})
	case "set-seen":
		// patch.Path contains UID of peer who has seen stuff
		// patch.Value empty
		// patch.OldValue empty
		if ctx.uid != patch.Path {
			return fmt.Errorf("cannot set seen for user other than context user. %s != %s ", ctx.uid, patch.Path)
		}
		if err := backend.pokeTimers(nid, false, ctx); err != nil {
			log.Printf("notesqlbackend: note(%s) couldnot poke edit-timers for uid %s. err: %s", nid, ctx.uid, err)
		}
		result.Tainted(Resource{Kind: "note", ID: nid})
	case "change-peer-uid":
		// patch.Path contains old peer's UID
		// patch.Value new peer's UID
		// patch.OldValue empty
		res, err := backend.db.Exec("UPDATE noterefs SET uid = ? WHERE nid = ? AND uid = ?", patch.Value, nid, patch.Path)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			ctx.Router.Handle(Event{UID: patch.Value.(string), Name: "res-add", Res: Resource{Kind: "note", ID: nid}, ctx: ctx})
			ctx.Router.Handle(Event{UID: patch.Path, Name: "res-remove", Res: Resource{Kind: "note", ID: nid}, ctx: ctx})
			// n.b. we're omitting the res-taint for the previous owner's folio here.
			// this method is supposed to be used for takeover of anon-session's notes
			// on login/signup so the old session and user get discarded and never used again.
			result.Tainted(Resource{Kind: "folio", ID: patch.Value.(string)})
			result.Tainted(Resource{Kind: "note", ID: nid})
		}
	}
	return nil
}

func (backend NoteSQLBackend) CreateEmpty(ctx Context) (string, error) {
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

func (backend NoteSQLBackend) patchText(id string, patch []DMP.Patch, result *SyncResult) error {
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
	if original != patched {
		result.Tainted(Resource{Kind: "note", ID: id})
	}
	return nil
}

func (backend NoteSQLBackend) pokeTimers(id string, edited bool, ctx Context) (err error) {
	if edited {
		_, err = backend.db.Exec("UPDATE noterefs SET last_seen = datetime('now'), last_edit = datetime('now') WHERE nid = ? AND uid = ?", id, ctx.uid)
	} else {
		_, err = backend.db.Exec("UPDATE noterefs SET last_seen = datetime('now') WHERE nid = ? AND uid = ?", id, ctx.uid)
	}
	return
}

func (backend NoteSQLBackend) sendInvite(user User, nid string, ctx Context) {
	rcpt := preferredRcpt(user)
	token, hashed := generateToken()
	reqData := map[string]string{"token": token}
	// get info from inviter
	res := Resource{Kind: "profile", ID: ctx.uid}
	inviter := User{}
	if err := ctx.store.Load(&res); err != nil {
		log.Printf("error: sendInvite could not fetch profile info of inviter; err: %v", err)
	} else {
		inviter = res.Value.(Profile).User
	}
	// store hashed token and recipient-address
	var err error
	switch addr, addrKind := rcpt.Addr(); addrKind {
	case "phone":
		reqData["inviter_name"] = firstNonEmpty(inviter.Name, "Anonymous")
		_, err = backend.db.Exec("INSERT INTO tokens (token, kind, uid, nid, phone) VALUES (?, 'share', ?, ?, ?)", hashed, user.UID, nid, addr)
	case "email":
		reqData["inviter_name"] = firstNonEmpty(inviter.Name, "Anonymous")
		_, err = backend.db.Exec("INSERT INTO tokens (token, kind, uid, nid, email) VALUES (?, 'share', ?, ?, ?)", hashed, user.UID, nid, addr)
	default:
		log.Printf("warn: cannot invite user[%s]. no usable contanct-addr found", user.UID)
		return
	}
	if err != nil {
		log.Printf("error: sendInvite failed at storing a token - aborting invite; err: %v", err)
		return
	}

	// collect info about the shared note
	note := NewNote("")
	res = Resource{Kind: "note", ID: nid}
	if err = ctx.store.Load(&res); err != nil {
		log.Printf("error: sendInvite could not fetch note info of shared note; err: %v", err)
	} else {
		note = res.Value.(Note)
	}
	if len(note.Text) > 500 {
		reqData["peek"] = string(note.Text)[:500]
	} else {
		reqData["peek"] = string(note.Text)
	}
	reqData["title"] = note.Title
	reqData["num_peers"] = strconv.Itoa(len(note.Peers))
	req := comm.NewRequest("invite", rcpt, reqData)
	if err = ctx.store.commHandler(req); err != nil {
		log.Printf("error: sendInvite could not forward request to comm.Handler; err: %v", err)
	}
}

func generateNID() string {
	return randomString(10)
}
