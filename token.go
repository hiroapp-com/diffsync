package diffsync

import (
	"crypto/rand"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"
	"time"

	"database/sql"
)

var tokenLifeTimes = map[string]time.Duration{
	"anon": 5 * time.Minute,
	//"login":     5 * 24 * time.Hour,
	"login":     2 * 7 * 24 * time.Hour,    //2 weeks for now, so that sent-out login tokens to alpha users live a little longer
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
			event.ctx.LogInfo("Consumation of token `%s` failed with error: %s", event.Token, err)
			if r, ok := err.(Remark); ok {
				event.Remark = &r
			} else {
				event.Remark = &Remark{Level: "error", Slug: "system-error"}
			}
			event.ctx.Client.Handle(event)
			return nil
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
			event.ctx.LogInfo("Consumation of token `%s` failed with error: %s", event.Token, err)
			if r, ok := err.(Remark); ok {
				event.Remark = &r
			} else {
				event.Remark = &Remark{Level: "error", Slug: "system-error"}
			}
			event.ctx.Client.Handle(event)
			return nil
		}
		event.ctx.sid = event.SID
		session, err := tok.consumeToken(token, event.ctx)
		if err != nil {
			return err
		}
		event.ctx.sid = session.sid
		event.ctx.uid = session.uid
		event.SID = session.sid
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
			if _, err = tok.db.Exec("UPDATE users SET email_status = 'verified' WHERE uid = $1", token.UID); err != nil {
				return nil, err
			}
		} else if token.Phone == u.Phone && u.PhoneStatus == "unverified" {
			if _, err = tok.db.Exec("UPDATE users SET phone_status = 'verified' WHERE uid = $1", token.UID); err != nil {
				return nil, err
			}
		}
		if u.Tier < 0 {
			if _, err = tok.db.Exec("UPDATE users SET tier = 0 WHERE uid = $1", token.UID); err != nil {
				return nil, err
			}
			ctx.Router.Handle(Event{Name: "res-sync", Res: Resource{Kind: "profile", ID: u.UID}, ctx: ctx})
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
		oldUID, tier, err := tok.uidFromSID(ctx.sid)
		if err != nil {
			return nil, err
		}
		// only merge if old user wasn't re-used on signup and if old user is anon
		if oldUID != uid && tier == 0 {
			if err = tok.assimilateUser(oldUID, uid, ctx); err != nil {
				return nil, err
			}
			ctx.Router.Handle(Event{Name: "res-sync", Res: Resource{Kind: "folio", ID: uid}, ctx: ctx})
			ctx.Router.Handle(Event{Name: "res-sync", Res: Resource{Kind: "profile", ID: uid}, ctx: ctx})
		}
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
	if token.NID != "" {
		// check if consuming session already has token.NID note
		for i := range session.shadows {
			if session.shadows[i].res.SameRef(Resource{Kind: "note", ID: token.NID}) {
				// already in consuming session's folio. don't re-add and dont
				// consume token
				return session, nil
			}
		}
		if err = tok.addNoteRef(session.uid, token.NID, ctx); err != nil {
			return nil, err
		}
	}
	if err = tok.markConsumed(token); err != nil {
		return nil, err
	}
	return session, nil
}

func (tok *TokenConsumer) GetUID(sid string) (string, error) {
	return tok.sessions.GetUID(sid)
}

func (tok *TokenConsumer) markConsumed(token Token) (err error) {
	_, err = tok.db.Exec("UPDATE tokens SET times_consumed = times_consumed+1, last_consumed_at=now() WHERE token = $1", token.Key)
	return
}

func (tok *TokenConsumer) getToken(plain string) (Token, error) {
	h := sha512.New()
	io.WriteString(h, plain)
	hashed := hex.EncodeToString(h.Sum(nil))
	t := Token{}
	err := tok.db.QueryRow("SELECT token, kind, uid, nid, email, phone, valid_from, times_consumed FROM tokens where token = $1", hashed).Scan(&t.Key, &t.Kind, &t.UID, &t.NID, &t.Email, &t.Phone, &t.ValidFrom, &t.TimesConsumed)
	if err == sql.ErrNoRows {
		return Token{}, Remark{Level: "error", Slug: "token-noexist-or-invalid"}
	} else if err != nil {
		return Token{}, err
	}
	if t.Expired() {
		return Token{}, Remark{Level: "error", Slug: "token-expired"}
	}
	if t.Exhausted() {
		return Token{}, Remark{Level: "error", Slug: "token-exhausted", Data: map[string]string{"max-consumes": strconv.Itoa(int(t.TimesConsumed))}}
	}
	log.Printf("retrieved token from db: %v\n", t)
	return t, nil
}

func (tok *TokenConsumer) uidFromSID(sid string) (uid string, tier int, err error) {
	err = tok.db.QueryRow("SELECT sessions.uid, users.tier FROM sessions LEFT JOIN users ON users.uid = sessions.uid WHERE sid = $1", sid).Scan(&uid, &tier)
	if err == sql.ErrNoRows {
		// return no error but empty uid if no result
		err = nil
		uid = ""
		tier = 0
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
	rs, err := txn.Query("SELECT nid FROM noterefs WHERE uid = $1", uidMan)
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
	if _, err = txn.Exec("UPDATE noterefs SET uid = $1 WHERE uid = $2", uidBorg, uidMan); err != nil {
		txn.Rollback()
		return err
	}
	// also claim all his contacts...
	if _, err = txn.Exec("UPDATE contacts SET uid = $1 WHERE uid = $2", uidBorg, uidMan); err != nil {
		txn.Rollback()
		return err
	}
	// ...symmetrically
	if _, err = txn.Exec("UPDATE contacts SET contact_uid = $1 WHERE contact_uid = $2", uidBorg, uidMan); err != nil {
		txn.Rollback()
		return err
	}
	// copy name
	if _, err = txn.Exec("UPDATE users SET name = (select name from users WHERE uid = $1 limit 1) WHERE uid = $2 AND name = ''", uidMan, uidBorg); err != nil {
		txn.Rollback()
		return err
	}
	// mark all other users with his email as disabled (tier -2)
	if _, err = txn.Exec("UPDATE users SET tier = -2 WHERE uid = $1", uidMan); err != nil {
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
	rs, err := txn.Query(f("SELECT uid, nid FROM noterefs WHERE uid IN (SELECT uid FROM users WHERE uid <> $1 AND $FIELD$ = $2)"), user.UID, v)
	if err != nil {
		txn.Rollback()
		return err
	}
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
	if _, err = txn.Exec(f("UPDATE noterefs SET uid = $1 WHERE uid IN (select uid from users WHERE uid <> cast($1 as varchar) and $FIELD$ = $2)"), user.UID, v); err != nil {
		txn.Rollback()
		return err
	}
	// also claim all his contacts...
	if _, err = txn.Exec(f("UPDATE contacts SET uid = $1 WHERE uid IN (select uid from users WHERE uid <> cast($1 as varchar) and $FIELD$ = $2)"), user.UID, v); err != nil {
		txn.Rollback()
		return err
	}
	// ...symmetrically
	if _, err = txn.Exec(f("UPDATE contacts SET contact_uid = $1 WHERE contact_uid IN (select uid from users WHERE uid <> cast($1 as varchar) and $FIELD$ = $2)"), user.UID, v); err != nil {
		txn.Rollback()
		return err
	}
	// mark all other users with his email as disabled (tier -2)
	if _, err = txn.Exec(f("UPDATE users SET tier = -2 WHERE uid IN (select uid from users WHERE uid <> cast($1 as varchar) and $FIELD$ = $2)"), user.UID, v); err != nil {
		txn.Rollback()
		return err
	}
	// and set claiming user's email status to verified
	if _, err = txn.Exec(f("UPDATE users SET $FIELD$_status = 'verified' WHERE uid = $1"), user.UID); err != nil {
		txn.Rollback()
		return err
	}
	// sign him up!
	if _, err = txn.Exec("UPDATE users SET tier = 1 WHERE uid = $1", user.UID); err != nil {
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
