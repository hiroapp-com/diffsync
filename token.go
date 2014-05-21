package diffsync

import (
	"fmt"
	"log"
)

// Public interface for checking and consuming auth-tokens.
// A TokenConsumer should be able to check if a given token
// is valid and return its properties (e.g. associated user-id)
// and the Resources accessible by that token.
// Consuming a token also implies that the TokenConsumer
// will create a new session (based on the tokens Resource capabilities)
// if needed.
// TokenConsumer.Consume can also receive a consumer-session ID
// If provided (and the session is still valid), the consumer should
// merge "new" resources from the token's capabiliies to the current
// session.
// If the provided session is owned by a hiro-user, the consumer should
// also send a  patch to the users folio-resource, so that the addition will
// be propagated to all other current user-sessions.
type TokenConsumer interface {
	Token(string) (*Token, error)
	Consume(string, string) (string, error)
}

type Token struct {
	Key       string
	UserID    string
	Resources []Resource
}

type TokenDoesNotexistError string

func (err TokenDoesNotexistError) Error() string {
	return fmt.Sprintf("token `%s` does not exist")
}

type HiroTokens struct {
	sessions SessionBackend
	stores   map[string]*Store
	Tokens   map[string]Token
}

func NewHiroTokens(backend SessionBackend, stores map[string]*Store) *HiroTokens {
	return &HiroTokens{backend, stores, make(map[string]Token)}
}

func (hirotok *HiroTokens) Token(key string) (*Token, error) {
	token, ok := hirotok.Tokens[key]
	if !ok {
		return nil, TokenDoesNotexistError(key)
	}
	return &token, nil
}

func (hirotok *HiroTokens) Consume(token_key string, sid string) (string, error) {
	log.Printf("consuming token `%s` (for sid `%s`)", token_key, sid)
	token, err := hirotok.Token(token_key)
	if err != nil {
		return "", err
	}
	var session *Session
	if sid != "" {
		log.Printf("session (%s) exists, loading from backend", sid)
		session, err = hirotok.sessions.Get(sid)
		if err != nil {
			// todo check if session has expired or anyhing
			// maybe we want to proceed normaly with token
			// even if provided session is dead for some reason
			return "", err
		}
	}
	modified := false
	if session == nil {
		// create session
		session = NewSession(token.UserID)
		modified = true
		log.Printf("created new Session `%s`", session.id)
	}
	var store *Store
	for _, res := range session.diff_resources(token.Resources) {
		// load current value
		log.Printf("loading add adding new resource to session `%s`: `%v`\n", session.id, res)
		newres := res.Ref()
		store = hirotok.stores[newres.Kind]
		if store == nil {
			return "", fmt.Errorf("unknown resource kind: `%s`", newres.Kind)
		}
		err := store.Load(&newres)
		if err != nil {
			//todo get rid of panic
			panic(err)
		}
		session.shadows[newres.StringRef()] = NewShadow(newres, session.id)
		modified = true
	}
	if modified {
		err = hirotok.sessions.Save(session)
		if err != nil {
			//whyever that would happen
			return "", err
		}
	}
	// session created, return new sid and leave consume()
	return session.id, nil
}
