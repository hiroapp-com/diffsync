package diffsync

import (
	"crypto/rand"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"database/sql"
)

type TokenConsumer interface {
	CreateSession(string, string, *Store) (*Session, error)
	Consume(string, string, *Store) (*Session, error)
	GetUID(string) (string, error)
}

type Token struct {
	Key        string
	Kind       string
	UID        string
	NID        string
	Email      string
	Phone      string
	CreatedAt  string
	ConsumedAt string
}

type TokenDoesNotexistError string

type HiroTokens struct {
	db       *sql.DB
	hub      *SessionHub
	sessions SessionBackend
}

func NewHiroTokens(backend SessionBackend, hub *SessionHub, db *sql.DB) *HiroTokens {
	return &HiroTokens{db, hub, backend}
}

func (tok *HiroTokens) CreateSession(token_key, oldSID string, store *Store) (*Session, error) {
	log.Printf("creating new session, using token `%s`", token_key)
	token, err := tok.getToken(token_key)
	if err != nil {
		return nil, err
	}
	sid := generateSID()
	var profile Resource
	switch token.Kind {
	case "anon", "share-email", "share-phone", "share-url":
		// anon token
		// create new blank user
		profile, err = store.NewResource("profile", context{sid: sid})
		if err != nil {
			return nil, err
		}
	case "login", "verify":
		// login token
		// load token's user
		profile = Resource{Kind: "profile", ID: token.UID}
		if err = store.Load(&profile); err != nil {
			return nil, err
		}
	default:
		panic("invalid token.Kind received: " + token.Kind)
	}
	uid := profile.Value.(Profile).User.UID
	session := NewSession(sid, uid)
	// token referenced NID, add to session user's folio
	if token.NID != "" {
		if changed, err := tok.addNoteRef(uid, token.NID); err != nil {
			return nil, err
		} else if changed {
			store.NotifyTaint("note", token.NID, context{"", uid, time.Now()})
		}
	}
	// merge old session's data
	if oldSID != "" {
		oldSession, err := tok.hub.Snapshot(oldSID)
		switch err := err.(type) {
		default:
			return nil, err
		case ResponseTimeoutErr:
			// we couldn't load the sessin due to
			// slow/unresponsive session. if we ignore this,
			// old session data might get lost.
			// instead, fail hard and let the client retry later
			log.Printf("token: FATAL backend timed out during snapshot request. sid: `%s`", oldSID)
			return nil, err
		case SessionIDInvalidErr:
			// provided SID is (for some reason) considered invalid
			// we will simply ignore the sid and not import any data,
			// but proceed normally
		case nil:
		}
		sessProfile := Resource{Kind: "profile", ID: oldSession.uid}
		if err = store.Load(&sessProfile); err != nil {
			return nil, err
		}
		if sessProfile.Value.(Profile).User.Tier == 0 {
			// only merge data from anon sessions
			for _, shadow := range oldSession.shadows {
				// only extract notes
				if shadow.res.Kind == "note" {
					if changed, err := tok.stealNoteRef(oldSession.uid, uid, shadow.res.ID); err != nil {
						return nil, err
					} else if changed {
						store.NotifyTaint("note", shadow.res.ID, context{session.sid, session.uid, time.Now()})
					}
				}
			}
		}
	}
	// TODO(flo) if kind == share-email, share-phone: load invite-users notes
	// see if there's an invite-user with verified email/phone.
	// if yes, merge data over, verify own email and remove invite-user
	if token.Kind == "verify" {
		newNIDs := []string{}
		switch {
		case token.Email != "":
			if err := tok.verifyID(uid, "email", token.Email); err == nil {
				newNIDs, _ = tok.sweepInvites(uid, "email", token.Email)
			}
		case token.Phone != "":
			if err := tok.verifyID(uid, "phone", token.Phone); err == nil {
				newNIDs, _ = tok.sweepInvites(uid, "phone", token.Phone)
			}
		}
		for i := range newNIDs {
			store.NotifyReset("note", newNIDs[i], context{session.sid, session.uid, time.Now()})
		}
	}

	// Finally, load users folio
	folio := Resource{Kind: "folio", ID: uid}
	if err := store.Load(&folio); err != nil {
		return nil, err
	}
	session.addShadow(profile)
	session.addShadow(folio)
	// load notes and mount shadows
	for _, ref := range folio.Value.(Folio) {
		log.Printf("loading add adding new note to session[%s]: `%s`\n", session.sid, ref.NID)
		res := Resource{Kind: "note", ID: ref.NID}
		if err := store.Load(&res); err != nil {
			return nil, err
		}
		session.addShadow(res)
	}
	if err = tok.sessions.Save(session); err != nil {
		return nil, err
	}
	return session, nil
}

func (tok *HiroTokens) Consume(token_key, sid string, store *Store) (*Session, error) {
	log.Printf("consuming token `%s` (for sid `%s`)", token_key, sid)
	token, err := tok.getToken(token_key)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(token.Kind, "share") {
		return nil, errors.New("cannot consume non-shareing token" + token.Kind)
	}
	log.Printf("loading session (%s) from hub", sid)
	session, err := tok.hub.Snapshot(sid)
	if err != nil {
		// todo check if session has expired or anyhing
		// maybe we want to proceed normaly with token
		// even if provided session is dead for some reason
		return nil, err
	}
	if token.NID == "" {
		// token is not a note-sharing token. unable to consume
		return nil, errors.New("note-id is missing in token info (how can that happen?). aborting")
	}
	added, err := tok.addNoteRef(session.uid, token.NID)
	if err != nil {
		return nil, err
	}
	if added {
		// notify the session that its folio has changed
		store.NotifyTaint("folio", session.uid, context{session.sid, session.uid, time.Now()})
		// TODO: can we address this reset directly to session.sid?
		store.NotifyReset("note", token.NID, context{session.sid, session.uid, time.Now()})
		store.NotifyTaint("note", token.NID, context{session.sid, session.uid, time.Now()})
	}
	return session, nil
}

func (tok *HiroTokens) GetUID(sid string) (string, error) {
	return tok.sessions.GetUID(sid)
}

func (tok *HiroTokens) getToken(plain string) (Token, error) {
	h := sha512.New()
	io.WriteString(h, plain)
	hashed := hex.EncodeToString(h.Sum(nil))
	log.Printf("Looking for token (byte: `%v`) with hash %s", tok, hashed)
	token := Token{}
	err := tok.db.QueryRow("SELECT token, kind, uid, nid, email, phone FROM tokens where token = ? AND consumed_at IS NULL", hashed).Scan(&token.Key, &token.Kind, &token.UID, &token.NID, &token.Email, &token.Phone)
	if err == sql.ErrNoRows {
		return Token{}, TokenDoesNotexistError(plain)
	} else if err != nil {
		return Token{}, err
	}
	log.Printf("retrieved token from db: %v\n", token)
	return token, nil
}

func (tok *HiroTokens) addNoteRef(uid, nid string) (changed bool, err error) {
	// TODO mayb we can refactor this part to instead of directly modifying the DB
	// we send an appropriate add-noteref patch down the wire to the folio-store and let the
	// machinery do the rest
	res, err := tok.db.Exec("INSERT INTO noterefs (uid, nid, status, role) VALUES (?, ?, 'active', 'active')", uid, nid)
	if err != nil {
		return false, err
	}
	numChanges, _ := res.RowsAffected()
	return (numChanges > 0), nil
}

func (tok *HiroTokens) stealNoteRef(from_uid, to_uid, nid string) (changed bool, err error) {
	// TODO mayb we can refactor this part to instead of directly modifying the DB
	// we send an appropriate add-noteref patch down the wire to the folio-store and let the
	// machinery do the rest
	res, err := tok.db.Exec("UPDATE noterefs SET uid = ? WHERE nid = ? AND uid = ?", to_uid, nid, from_uid)
	if err != nil {
		return false, err
	}
	numChanges, _ := res.RowsAffected()
	return (numChanges > 0), nil
}

func (tok *HiroTokens) verifyID(uid, kind, to_verify string) error {
	switch kind {
	case "email", "phone":
	default:
		return errors.New("only `email` or `phone` verifications supported")
	}
	injectKind := func(qry string) string {
		return strings.Replace(qry, "KIND", kind, -1)
	}
	res, err := tok.db.Exec(injectKind(`UPDATE users SET KIND_status = 'verified' 
						WHERE uid = ? 
						AND KIND = ?
						AND (SELECT count(*) FROM users where KIND = ? and KIND_status = 'verified') = 0`), uid, to_verify, to_verify)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		// query didnt go trhough, means we could not verify anything.
		return fmt.Errorf("coul not verify email for uid %s email/phone %s", uid, to_verify)
	}
	return nil
}
func (tok *HiroTokens) sweepInvites(uid, kind, to_verify string) (invitedNIDs []string, err error) {
	// now see if there's an invite user with given ID dangling around
	invitedNIDs = []string{}
	txn, err := tok.db.Begin()
	if err != nil {
		return
	}
	injectKind := func(qry string) string {
		return strings.Replace(qry, "KIND", kind, -1)
	}
	rows, err := txn.Query(injectKind("SELECT nid FROM noterefs where uid IN (SELECT uid from users WHERE KIND = ? and KIND_status = 'invited')"), to_verify)
	if err == sql.ErrNoRows {
		log.Println("none found")
		txn.Commit()
		return
	} else if err != nil {
		txn.Rollback()
		return
	}
	stmt, err := txn.Prepare("INSERT INTO noterefs (nid, uid, status, role) VALUES (?, ?, 'active', 'active')")
	if err != nil {
		rows.Close()
		txn.Rollback()
		return
	}
	defer stmt.Close()
	for rows.Next() {
		var nid string
		if err = rows.Scan(&nid); err != nil {
			txn.Rollback()
			return
		}
		var res sql.Result
		if res, err = stmt.Exec(nid, uid); err != nil {
			txn.Rollback()
			return
		}
		if affected, _ := res.RowsAffected(); affected > 0 {
			invitedNIDs = append(invitedNIDs, nid)
		}
	}
	_, err = txn.Exec(injectKind("UPDATE users SET KIND_status = 'consumed' WHERE KIND = ? AND KIND_status = 'invited'"), to_verify)
	if err != nil {
		txn.Rollback()
		return
	}
	txn.Commit()
	return

}

func (err TokenDoesNotexistError) Error() string {
	return fmt.Sprintf("token `%s` invalid or expired", string(err))
}

func generateToken() (string, string) {
	uuid := make([]byte, 16)
	if n, err := rand.Read(uuid); err != nil || n != len(uuid) {
		panic(err)
	}
	// RFC 4122
	uuid[8] = 0x80 // variant bits
	uuid[4] = 0x40 // v4
	plain := hex.EncodeToString(uuid)
	h := sha512.New()
	io.WriteString(h, plain)
	hashed := hex.EncodeToString(h.Sum(nil))
	return plain, hashed
}
