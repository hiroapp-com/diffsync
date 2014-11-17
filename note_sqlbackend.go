package diffsync

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"database/sql"

	"bitbucket.org/sushimako/hync/comm"
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
	err := backend.db.QueryRow("SELECT title, txt, sharing_token FROM notes WHERE nid = $1", key).Scan(&note.Title, &txt, &note.SharingToken)
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
										  nr.cursor_pos, 
										  nr.last_seen, 
										  nr.last_edit, 
										  nr.role 
								    FROM noterefs as nr 
									  LEFT JOIN users as u1
									    ON u1.uid = nr.uid
									WHERE nid = $1 
									  AND tier > -2`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		peer := Peer{User: User{}}
		if err := rows.Scan(&peer.User.UID, &peer.User.Tier, &peer.CursorPosition, &peer.LastSeen, &peer.LastEdit, &peer.Role); err != nil {
			return nil, err
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
		res, err := backend.db.Exec("UPDATE notes SET title = $1 WHERE nid = $2 and title = $3", patch.Value.(string), nid, patch.OldValue.(string))
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
		res, err := backend.db.Exec("INSERT INTO noterefs (nid, uid, role, status) VALUES($1, $2, 'peer', 'active')", nid, u.UID)
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
		_, err := backend.db.Exec("UPDATE noterefs SET cursor_pos = $1 WHERE nid = $2 AND uid = $3 AND cursor_pos = $4", patch.Value.(int64), nid, patch.Path, patch.OldValue.(int64))
		if err != nil {
			return err
		}
		result.Tainted(Resource{Kind: "note", ID: nid})
	case "rem-peer":
		// patch.Path contains UID of peer to remove
		// patch.Value empty
		// patch.OldValue empty
		_, err := backend.db.Exec("DELETE FROM noterefs WHERE nid = $1 AND uid = $2", nid, patch.Path)
		if err != nil {
			return err
		}
		_, err = backend.db.Exec("DELETE FROM tokens WHERE kind = 'share' AND nid = $1 AND uid = $2", nid, patch.Path)
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
	}
	return nil
}

func (backend NoteSQLBackend) CreateEmpty(ctx Context) (string, error) {
	note := NewNote("")
	nid := generateNID()
	token, hashed := generateToken()
	_, err := backend.db.Exec("INSERT INTO notes (nid, title, txt, sharing_token, created_by) VALUES ($1, $2, $3, $4, $5)", nid, note.Title, string(note.Text), token, ctx.uid)
	if err != nil {
		return "", err
	}
	// cleanup, fire and forget
	backend.db.Exec("DELETE FROM tokens WHERE uid = '' and nid = $1 and kind = 'share'", nid)
	// create token
	if _, err := backend.db.Exec("INSERT INTO tokens (token, kind, uid, nid) VALUES ($1, 'share-url', '', $2)", hashed, nid); err != nil {
		return "", nil
	}
	// already create a noteref (i.e. add to user's folio) if uid provided in context
	if ctx.uid != "" {
		backend.db.Exec("INSERT INTO noterefs (uid, nid, status, role) VALUES ($1, $2, 'active', 'owner')", ctx.uid, nid)
	}
	return nid, nil
}

func (backend NoteSQLBackend) patchText(id string, patch []DMP.Patch, result *SyncResult) error {
	txn, err := backend.db.Begin()
	if err != nil {
		return err
	}
	var original string
	switch err := txn.QueryRow("SELECT txt FROM notes WHERE nid = $1", id).Scan(&original); {
	case err == sql.ErrNoRows:
		txn.Rollback()
		return NoExistError{id}
	case err != nil:
		txn.Rollback()
		return err
	}
	patched, _ := dmp.PatchApply(patch, original)
	if _, err := txn.Exec("UPDATE notes SET txt = $1 WHERE nid = $2", patched, id); err != nil {
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
		_, err = backend.db.Exec("UPDATE noterefs SET last_seen = NOW(), last_edit = NOW() WHERE nid = $1 AND uid = $2", id, ctx.uid)
	} else {
		_, err = backend.db.Exec("UPDATE noterefs SET last_seen = NOW() WHERE nid = $1 AND uid = $2", id, ctx.uid)
	}
	return
}

func (backend NoteSQLBackend) sendInvite(user User, nid string, ctx Context) {
	token, hashed := generateToken()
	reqData := map[string]string{"token": token, "nid": nid}
	// get info from inviter
	res := Resource{Kind: "profile", ID: ctx.uid}
	if err := ctx.store.Load(&res); err != nil {
		log.Printf("error: sendInvite could not fetch profile info of inviter; err: %v", err)
	}
	inviter := res.Value.(Profile).User
	reqData["inviter_name"] = inviter.Name
	reqData["inviter_email"] = inviter.Email
	reqData["inviter_phone"] = inviter.Phone
	// store hashed token and recipient-address
	var err error
	switch addr, addrKind := user.Addr(); addrKind {
	case "phone":
		_, err = backend.db.Exec("INSERT INTO tokens (token, kind, uid, nid, phone) VALUES ($1, 'share', $2, $3, $4)", hashed, user.UID, nid, addr)
	case "email":
		_, err = backend.db.Exec("INSERT INTO tokens (token, kind, uid, nid, email) VALUES ($1, 'share', $2, $3, $4)", hashed, user.UID, nid, addr)
	default:
		log.Printf("warn: cannot invite user[%s]. no usable contanct-addr found", user.UID)
		return
	}
	if err != nil {
		log.Printf("error: sendInvite failed at storing a token - aborting invite; err: %v", err)
		return
	}

	// collect info about the shared note
	res = Resource{Kind: "note", ID: nid}
	if err = ctx.store.Load(&res); err != nil {
		log.Printf("error: sendInvite could not fetch note info of shared note; err: %v", err)
	}
	note := res.Value.(Note)
	if txt := string(note.Text); len(txt) > 500 {
		if i := strings.LastIndex(txt[:500], " "); i < 0 {
			reqData["peek"] = txt[:500] + "..."
		} else {
			reqData["peek"] = txt[:i] + " ..."
		}
	} else {
		reqData["peek"] = txt
	}
	reqData["title"] = note.Title
	reqData["num_peers"] = strconv.Itoa(len(note.Peers))
	reqData["invitee_tier"] = strconv.Itoa(int(user.Tier))
	req := comm.NewRequest("invite", user, reqData)
	if err = ctx.store.commHandler(req); err != nil {
		log.Printf("error: sendInvite could not forward request to comm.Handler; err: %v", err)
	}
}

func generateNID() string {
	return randomString(10)
}
