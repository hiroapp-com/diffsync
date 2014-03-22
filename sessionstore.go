package diffsync

import (
	"encoding/json"
	"fmt"
)

type StoreBackend interface {
	Create(string, json.Marshaler) error
	Update(string, json.Marshaler) error
	Get(string, json.Unmarshaler) error
	Delete(string) error
}
type SessionStore interface {
	Get(string) (*Session, error)
	Consume(string, string) (string, error)
	Kill(sid string) error
	Token(string) (*Token, error)
}

type HiroSessions struct {
	backend StoreBackend
}

func (store *HiroSessions) Get(sid string) (*Session, error) {
	//note: leaky bucket would be nice for buffer-reuse
	session := Session{}
	if err := store.backend.Get(sid, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

type TokenDoesNotexistError string

func (err TokenDoesNotexistError) Error() string {
	return fmt.Sprintf("token `%s` does not exist")
}
func (store *HiroSessions) Token(key string) (*Token, error) {
	token, ok := tmpTokens[key]
	if !ok {
		return nil, TokenDoesNotexistError(key)
	}
	return &token, nil
}

func (store *HiroSessions) Consume(token_key string, sid string) (string, error) {
	token, err := store.Token(token_key)
	if err != nil {
		return "", err
	}
	var session *Session
	if sid != "" {
		session, err = store.Get(sid)
		if err != nil {
			// todo check if session has expired or anyhing
			// maybe we want to proceed normaly with token
			// even if provided session is dead for some reason
			return "", err
		}
	}
	if session != nil {
		// if existing session, merge resources from token into session
		// and do some other magic
		// todo
		return session.id, nil
	}
	// create session
	session = NewSession(token.userid, token.resources)
	err = store.backend.Create(session.id, session)
	if err != nil {
		//whyever that would happen
		return "", err
	}
	// session created, return new sid and leave consume()
	return sid, nil
}

type Token struct {
	value     string
	userid    string
	resources []Resource
}

//tmpNotes := map[string]NoteValue{
//    "ak8Sk": NoteValue("HEEYEAAAA WELCOME TO TEH INVITES"),
//
//}

var tmpTokens = map[string]Token{
	"anon": {
		value:  "anon",
		userid: "",
		resources: []Resource{
			Resource{kind: "note", id: "ak8Sk", ResourceValue: NewNoteValue("HEEYEAAAA WELCOME TO TEH INVITES")},
			Resource{kind: "meta", id: "ak8Sk", ResourceValue: &MetaValue{}},
		},
	},
	"userlogin": {
		value:  "userlogin",
		userid: "sk80Ms",
		resources: []Resource{
			//Resource{kind: "folio", id:"sk80Ms", ResourceValue: FolioValue{} },
			//Resource{kind: "contacts", id:"sk80Ms", ResourceValue: ContactsValue{} },
			Resource{kind: "note", id: "ak8Sk", ResourceValue: NewNoteValue("HEEYEAAAA WELCOME TO TEH INVITES")},
			Resource{kind: "note", id: "9sl9i", ResourceValue: NewNoteValue("apples, bananas, kiwis, ginger")},
			Resource{kind: "note", id: "Hlq8l", ResourceValue: NewNoteValue("Tooodoooo")},
			Resource{kind: "meta", id: "ak8Sk", ResourceValue: &MetaValue{title: "shared fun"}},
			Resource{kind: "meta", id: "9sl9i", ResourceValue: &MetaValue{title: "grocery list"}},
			Resource{kind: "meta", id: "Hlq8l", ResourceValue: &MetaValue{title: "todos"}},
		},
	},
}
