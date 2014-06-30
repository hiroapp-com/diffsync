package diffsync

import (
	"errors"
	"fmt"
	"log"
	"time"

	"database/sql"
)

type TokenConsumer interface {
	CreateSession(string, *Store) (*Session, error)
	Consume(string, string, *Store) (*Session, error)
	GetUID(string) (string, error)
}

type Token struct {
	Key        string `sql:"token"`
	UID        string `sql:"uid"`
	NID        string `sql:"nid"`
	CreatedAt  string `sql:"created_at"`
	ConsumedAt string `sql:"consumed_at"`
}

type TokenDoesNotexistError string

type HiroTokens struct {
	db       *sql.DB
	sessions SessionBackend
}

func NewHiroTokens(backend SessionBackend, db *sql.DB) *HiroTokens {
	return &HiroTokens{db, backend}
}

func (tok *HiroTokens) CreateSession(token_key string, store *Store) (*Session, error) {
	log.Printf("creating new session, using token `%s`", token_key)
	token, err := tok.getToken(token_key)
	if err != nil {
		return nil, err
	}
	sid := sid_generate()
	var profile Resource
	if token.UID == "" {
		// anon token
		// create new blank user
		profile, err = store.NewResource("profile", context{sid: sid})
		if err != nil {
			return nil, err
		}
	} else if token.UID != "" && token.NID == "" {
		// login token
		// load token's user
		profile = Resource{Kind: "profile", ID: token.UID}
		if err = store.Load(&profile); err != nil {
			return nil, err
		}
	}
	uid := profile.Value.(Profile).User.UID
	session := NewSession(sid, uid)
	if token.NID != "" {
		if _, err := tok.addNoteRef(uid, token.NID); err != nil {
			return nil, err
		}
		store.NotifyTaint("note", token.NID, context{session.sid, session.uid, time.Now()})
	}
	folio := Resource{Kind: "folio", ID: uid}
	if err := store.Load(&folio); err != nil {
		return nil, err
	}
	session.addShadow(profile)
	session.addShadow(folio)
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
	log.Printf("loading session (%s) from backend", sid)
	session, err := tok.sessions.Get(sid)
	if err != nil {
		// todo check if session has expired or anyhing
		// maybe we want to proceed normaly with token
		// even if provided session is dead for some reason
		return nil, err
	}
	if token.NID == "" {
		// token is not a note-sharing token. unable to consume
		return nil, errors.New("received non note-sharing token. aborting")
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

func (tok *HiroTokens) getToken(key string) (Token, error) {
	token := Token{}
	err := tok.db.QueryRow("SELECT token, uid, nid FROM tokens where token = ? AND consumed_at IS NULL", key).Scan(&token.Key, &token.UID, &token.NID)
	if err == sql.ErrNoRows {
		return Token{}, TokenDoesNotexistError(key)
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

func (err TokenDoesNotexistError) Error() string {
	return fmt.Sprintf("token `%s` invalid or expired")
}
