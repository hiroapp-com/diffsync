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

var tokenLifeTimes = map[string]time.Duration{
	"login":     5 * time.Minute,
	"anon":      5 * time.Minute,
	"verify":    2 * 7 * 24 * time.Hour,    //2 weeks
	"share-url": 3 * 30.5 * 24 * time.Hour, //3 months
	"share":     1 * 30.5 * 24 * time.Hour, //1 month
}

type Token struct {
	Key           string
	Kind          string
	UID           string
	NID           string
	Email         string
	Phone         string
	ValidFrom     *time.Time
	TimesConsumed int64
}

func (t Token) Expired() bool {
	lt, _ := tokenLifeTimes[t.Kind]
	return time.Now().After(t.ValidFrom.Add(lt))
}

func (t Token) Exhausted() bool {
	if t.Kind == "share-url" {
		return t.TimesConsumed >= 10
	}
	return t.TimesConsumed > 0
}

type TokenDoesNotexistError string

type TokenConsumer struct {
	db       *sql.DB
	hub      *SessionHub
	sessions SessionBackend
}

func NewTokenConsumer(backend SessionBackend, hub *SessionHub, db *sql.DB) *TokenConsumer {
	return &TokenConsumer{db, hub, backend}
}

func (tok *TokenConsumer) Handle(event Event, next EventHandler) error {
	var session *Session
	switch event.Name {
	case "session-create":
		token, err := tok.getToken(event.Token)
		if err != nil {
			return err
		}
		event.ctx.sid = event.SID
		session, err = tok.createSession(token, event.ctx)
		if err != nil {
			return err
		}
		// save event.SID in context (if any was sent)
		if event.SID != "" {
			event.ctx.sid = event.SID
		} else {
			event.ctx.sid = session.sid
		}
		event.ctx.uid = session.uid
		event.SID = session.sid
		if err = tok.markConsumed(token); err != nil {
			return err
		}
	case "token-consume":
		token, err := tok.getToken(event.Token)
		if err != nil {
			return err
		}
		event.ctx.sid = event.SID
		session, err := tok.consumeToken(token, event.ctx)
		if err != nil {
			return err
		}
		event.ctx.sid = session.sid
		event.ctx.uid = session.uid
		event.SID = session.sid
		if err = tok.markConsumed(token); err != nil {
			return err
		}
	default:
		uid, err := tok.GetUID(event.SID)
		if err != nil {
			return err
		}
		event.ctx.uid = uid
		event.ctx.sid = event.SID
	}
	return next.Handle(event)
}

func (tok *TokenConsumer) createSession(token Token, ctx Context) (*Session, error) {
	store := ctx.store
	sid := generateSID()
	var profile Resource
	var err error
	switch token.Kind {
	case "anon", "share-url":
		profile, err = store.NewResource("profile", Context{sid: sid})
		if err != nil {
			return nil, err
		}
		// add note to folio, if any in token
		if err = tok.addNoteRef(profile.Value.(Profile).User.UID, token.NID, ctx); err != nil {
			return nil, err
		}
	case "share":
		profile = Resource{Kind: "profile", ID: token.UID}
		if err = store.Load(&profile); err != nil {
			return nil, err
		}
		u := profile.Value.(Profile).User
		if token.Email == u.Email && u.EmailStatus == "unverified" {
			if _, err = tok.db.Exec("UPDATE users SET email_status = 'verified' WHERE uid = ?", token.UID); err != nil {
				return nil, err
			}
		} else if token.Phone == u.Phone && u.PhoneStatus == "unverified" {
			if _, err = tok.db.Exec("UPDATE users SET phone_status = 'verified' WHERE uid = ?", token.UID); err != nil {
				return nil, err
			}
		}
		if u.Tier < 0 {
			if _, err = tok.db.Exec("UPDATE users SET tier = 0 WHERE uid = ?", token.UID); err != nil {
				return nil, err
			}
		}
		// add note to folio, if any in token
		if err = tok.addNoteRef(token.UID, token.NID, ctx); err != nil {
			return nil, err
		}
	case "verify":
		profile = Resource{Kind: "profile", ID: token.UID}
		if err = store.Load(&profile); err != nil {
			return nil, err
		}
		u := profile.Value.(Profile).User
		if token.Email == u.Email && u.EmailStatus == "unverified" {
			err = tok.claimIDAndSignup("email", u, ctx)
		}
		if token.Phone == u.Phone && u.PhoneStatus == "unverified" {
			err = tok.claimIDAndSignup("phone", u, ctx)
		}
		if err != nil {
			return nil, err
		}
		ctx.Router.Handle(Event{Name: "res-sync", Res: Resource{Kind: "profile", ID: u.UID}, ctx: ctx})
		ctx.Router.Handle(Event{Name: "res-sync", Res: Resource{Kind: "folio", ID: u.UID}, ctx: ctx})
	case "login":
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

	// merge old session's data
	if ctx.sid != "" {
		oldUID, err := tok.uidFromSID(ctx.sid, true)
		if err != nil {
			return nil, err
		}
		if err = tok.assimilateUser(oldUID, uid, ctx); err != nil {
			return nil, err
		}
		ctx.Router.Handle(Event{Name: "res-sync", Res: Resource{Kind: "folio", ID: uid}, ctx: ctx})
		ctx.Router.Handle(Event{Name: "res-sync", Res: Resource{Kind: "profile", ID: uid}, ctx: ctx})
	}

	// re-load profile
	if err := store.Load(&profile); err != nil {
		return nil, err
	}

	// load this sessions shadows
	folio := Resource{Kind: "folio", ID: uid}
	if err := store.Load(&folio); err != nil {
		return nil, err
	}
	session.shadows = append(session.shadows, NewShadow(profile), NewShadow(folio))
	// load notes and mount shadows
	// TODO should this happe in the sessionhandler? e.g. only send the session-create
	//  down and let the handle_session_create() do the rest, load all its info
	for _, ref := range folio.Value.(Folio) {
		log.Printf("loading note-shadow into session[%s]: `%s`\n", session.sid, ref.NID)
		res := Resource{Kind: "note", ID: ref.NID}
		if err := store.Load(&res); err != nil {
			return nil, err
		}
		session.shadows = append(session.shadows, NewShadow(res))
	}
	if err = tok.sessions.Save(session); err != nil {
		return nil, err
	}
	return session, nil
}

func (tok *TokenConsumer) consumeToken(token Token, ctx Context) (*Session, error) {
	if !strings.HasPrefix(token.Kind, "share") {
		return nil, errors.New("cannot consume non-sharing token" + token.Kind)
	}
	log.Printf("loading session (%s) from hub", ctx.sid)
	session, err := tok.hub.Snapshot(ctx.sid, ctx)
	if err != nil {
		// todo check if session has expired or anyhing
		// maybe we want to proceed normaly with token
		// even if provided session is dead for some reason
		return nil, err
	}
	err = tok.addNoteRef(session.uid, token.NID, ctx)
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (tok *TokenConsumer) GetUID(sid string) (string, error) {
	return tok.sessions.GetUID(sid)
}

func (tok *TokenConsumer) markConsumed(token Token) (err error) {
	_, err = tok.db.Exec("UPDATE tokens SET times_consumed = times_consumed+1 WHERE token = ?", token.Key)
	return
}

func (tok *TokenConsumer) getToken(plain string) (Token, error) {
	h := sha512.New()
	io.WriteString(h, plain)
	hashed := hex.EncodeToString(h.Sum(nil))
	log.Printf("Looking for token (byte: `%v`) with hash %s", tok, hashed)
	t := Token{}
	err := tok.db.QueryRow("SELECT token, kind, uid, nid, email, phone, valid_from, times_consumed FROM tokens where token = ?", hashed).Scan(&t.Key, &t.Kind, &t.UID, &t.NID, &t.Email, &t.Phone, &t.ValidFrom, &t.TimesConsumed)
	if err == sql.ErrNoRows {
		return Token{}, TokenDoesNotexistError(plain)
	} else if err != nil {
		return Token{}, err
	}
	if t.Expired() {
		return Token{}, fmt.Errorf("token has expired")
	}
	if t.Exhausted() {
		return Token{}, fmt.Errorf("token can only be consumed %d times", t.TimesConsumed)
	}
	log.Printf("retrieved token from db: %v\n", t)
	return t, nil
}

func (tok *TokenConsumer) uidFromSID(sid string, onlyAnon bool) (uid string, err error) {
	if onlyAnon {
		err = tok.db.QueryRow("SELECT sessions.uid FROM sessions LEFT JOIN users ON users.uid = sessions.uid WHERE sid = ? AND users.tier = 0", sid).Scan(&uid)
	} else {
		err = tok.db.QueryRow("SELECT sessions.uid FROM sessions WHERE sid = ?", sid).Scan(&uid)
	}
	if err == sql.ErrNoRows {
		// return no error but empty uid if no result
		err = nil
		uid = ""
	}
	return
}

func (tok *TokenConsumer) assimilateUser(uidMan, uidBorg string, ctx Context) error {
	if uidMan == "" {
		// assimilation is futile
		return nil
	}
	txn, err := tok.db.Begin()
	if err != nil {
		return err
	}
	// get all note-ids which we will take over, so we can taint them later
	rs, err := txn.Query("SELECT nid FROM noterefs WHERE uid = ?", uidMan)
	if err != nil {
		txn.Rollback()
		return err
	}
	nids := []string{}
	for rs.Next() {
		var nid string
		if err = rs.Scan(&nid); err != nil {
			txn.Rollback()
			return err
		}
		nids = append(nids, nid)
	}
	// now change those noterefs to the claiming UID
	if _, err = txn.Exec("UPDATE noterefs SET uid = ? WHERE uid = ?", uidBorg, uidMan); err != nil {
		txn.Rollback()
		return err
	}
	// also claim all his contacts...
	if _, err = txn.Exec("UPDATE contacts SET uid = ? WHERE uid = ?", uidBorg, uidMan); err != nil {
		txn.Rollback()
		return err
	}
	// ...symmetrically
	if _, err = txn.Exec("UPDATE contacts SET contact_uid = ? WHERE contact_uid = ?", uidBorg, uidMan); err != nil {
		txn.Rollback()
		return err
	}
	// mark all other users with his email as disabled (tier -2)
	if _, err = txn.Exec("UPDATE users SET tier = -2 WHERE uid = ?", uidMan); err != nil {
		txn.Rollback()
		return err
	}
	for i := range nids {
		ctx.Router.Handle(Event{UID: uidMan, Name: "res-remove", Res: Resource{Kind: "note", ID: nids[i]}, ctx: ctx})
		ctx.Router.Handle(Event{UID: uidBorg, Name: "res-add", Res: Resource{Kind: "note", ID: nids[i]}, ctx: ctx})
		ctx.Router.Handle(Event{Name: "res-sync", Res: Resource{Kind: "note", ID: nids[i]}, ctx: ctx})
	}
	txn.Commit()
	return nil
}
func (tok *TokenConsumer) claimIDAndSignup(id string, user User, ctx Context) error {
	txn, err := tok.db.Begin()
	if err != nil {
		return err
	}
	var v string
	switch id {
	case "email":
		v = user.Email
	case "phone":
		v = user.Phone
	default:
		return fmt.Errorf("invalid ID passed. can only claim 'phone' or 'email'")
	}
	f := func(qry string) string {
		return strings.Replace(qry, "$FIELD$", id, -1)
	}
	// get all note-ids which we will take over, so we can taint them later
	rs, err := txn.Query(f("SELECT uid, nid FROM noterefs WHERE uid IN (SELECT uid FROM users WHERE uid <> ? AND $FIELD$ = ?)"), user.UID, v)
	if err != nil {
		txn.Rollback()
		return err
	}
	// http://localhost:5000/#32c39993ee6746edb0883d88386fba9c
	nids := [][2]string{}
	for rs.Next() {
		var uid, nid string
		if err = rs.Scan(&uid, &nid); err != nil {
			txn.Rollback()
			return err
		}
		nids = append(nids, [2]string{uid, nid})
	}
	// now change those noterefs to the claiming UID
	if _, err = txn.Exec(f("UPDATE noterefs SET uid = ? WHERE uid IN (select uid from users WHERE uid <> ? and $FIELD$ = ?)"), user.UID, user.UID, v); err != nil {
		txn.Rollback()
		return err
	}
	// also claim all his contacts...
	if _, err = txn.Exec(f("UPDATE contacts SET uid = ? WHERE uid IN (select uid from users WHERE uid <> ? and $FIELD$ = ?)"), user.UID, user.UID, v); err != nil {
		txn.Rollback()
		return err
	}
	// ...symmetrically
	if _, err = txn.Exec(f("UPDATE contacts SET contact_uid = ? WHERE contact_uid IN (select uid from users WHERE uid <> ? and $FIELD$ = ?)"), user.UID, user.UID, v); err != nil {
		txn.Rollback()
		return err
	}
	// mark all other users with his email as disabled (tier -2)
	if _, err = txn.Exec(f("UPDATE users SET tier = -2 WHERE uid IN (select uid from users WHERE uid <> ? and $FIELD$ = ?)"), user.UID, v); err != nil {
		txn.Rollback()
		return err
	}
	// and set claiming user's email status to verified
	if _, err = txn.Exec(f("UPDATE users SET $FIELD$_status = 'verified' WHERE uid = ?"), user.UID); err != nil {
		txn.Rollback()
		return err
	}
	// sign him up!
	if _, err = txn.Exec("UPDATE users SET tier = 1 WHERE uid = ?", user.UID); err != nil {
		txn.Rollback()
		return err
	}
	for i := range nids {
		ctx.Router.Handle(Event{UID: nids[i][0], Name: "res-remove", Res: Resource{Kind: "note", ID: nids[i][1]}, ctx: ctx})
		ctx.Router.Handle(Event{UID: user.UID, Name: "res-add", Res: Resource{Kind: "note", ID: nids[i][1]}, ctx: ctx})
		ctx.Router.Handle(Event{Name: "res-sync", Res: Resource{Kind: "note", ID: nids[i][1]}, ctx: ctx})
	}
	txn.Commit()
	return nil
}

func (tok *TokenConsumer) addNoteRef(uid, nid string, ctx Context) error {
	if nid == "" {
		// no nid provided, nothin to add
		return nil
	}
	res := NewSyncResult()
	if err := ctx.store.Patch(Resource{Kind: "folio", ID: uid}, Patch{Op: "add-noteref", Value: NoteRef{NID: nid, Status: "active"}}, res, ctx); err != nil {
		return err
	}
	for _, r := range res.TaintedItems() {
		if err := ctx.Router.Handle(Event{Name: "res-sync", Res: r, ctx: ctx}); err != nil {
			return err
		}
	}
	return nil
}

func (tok *TokenConsumer) stealNoteRef(sid, uid string, ctx Context) error {
	s, err := tok.hub.Snapshot(sid, ctx)
	switch err := err.(type) {
	case ResponseTimeoutErr:
		// we couldn't load the sessin due to
		// slow/unresponsive session. if we ignore this,
		// old session data might get lost.
		// instead, fail hard and let the client retry later
		log.Printf("token: FATAL backend timed out during snapshot request. sid: `%s`", sid)
		return err
	case SessionIDInvalidErr:
		// provided SID is (for some reason) considered invalid
		// we will simply ignore the sid and not import any data,
		// but proceed normally
	default:
		return err
	case nil:
		// no error, continue
	}
	p := Resource{Kind: "profile", ID: s.uid}
	if err = ctx.store.Load(&p); err != nil {
		return err
	}
	if p.Value.(Profile).User.Tier != 0 {
		// only merge data from anon sessions
		return nil
	}
	res := NewSyncResult()
	for _, shadow := range s.shadows {
		// only extract notes
		if shadow.res.Kind == "note" {
			if err = ctx.store.Patch(shadow.res, Patch{Op: "change-peer-uid", Path: s.uid, Value: uid}, res, ctx); err != nil {
				return err
			}
		}
	}
	if len(res.TaintedItems()) > 0 {
		for _, res := range res.TaintedItems() {
			ctx.Router.Handle(Event{Name: "res-sync", Res: res, ctx: ctx})
		}
	}
	return nil
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
