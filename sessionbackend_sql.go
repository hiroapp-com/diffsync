package diffsync

import (
	"log"

	"database/sql"
)

type SQLSessions struct {
	db       *sql.DB
	sessbuff chan *Session
}

func NewSQLSessions(db *sql.DB) *SQLSessions {
	return &SQLSessions{
		db:       db,
		sessbuff: make(chan *Session, 256),
	}
}

func (store *SQLSessions) Get(sid string) (*Session, error) {
	session := store.allocateSession()
	err := store.db.QueryRow("SELECT data FROM sessions where sid = ?", sid).Scan(session)
	if err == sql.ErrNoRows {
		store.Release(session)
		return nil, InvalidSessionId{sid, SESSION_NOTEXIST}
	} else if err != nil {
		store.Release(session)
		return nil, err
	}
	return session, nil
}

func (store *SQLSessions) Save(session *Session) error {
	// is an upsert, needs doc
	log.Printf("saving session `%s`, %v", session.sid, *session)
	res, err := store.db.Exec("UPDATE sessions SET uid = ?, data = ? WHERE sid = ?", session.uid, session, session.sid)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows > 0 {
		// updated, all fine
		return nil
	}
	// nothing was updated, need to create session
	_, err = store.db.Exec("INSERT INTO sessions (sid, uid, data) VALUES (?, ?, ?)", session.sid, session.uid, session)
	return err
}

func (store *SQLSessions) Delete(sid string) error {
	log.Printf("deleting session `%s`", sid)
	_, err := store.db.Exec("DELETE FROM sessions WHERE sid = ?", sid)
	return err
}

func (store *SQLSessions) Release(sess *Session) {
	select {
	case store.sessbuff <- sess:
	default:
	}
}

func (store *SQLSessions) GetUID(sid string) (string, error) {
	sess, err := store.Get(sid)
	if err != nil {
		return "", err
	}
	return sess.uid, nil
}

func (store *SQLSessions) allocateSession() *Session {
	var sess *Session
	select {
	case sess = <-store.sessbuff:
		log.Printf("Reusing session-pointer: %v", sess)
	default:
		sess = new(Session)
	}
	return sess
}

func (store *SQLSessions) subsByQuery(qry string, args ...interface{}) ([]string, error) {
	rows, err := store.db.Query(qry, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	subs := []string{}
	for rows.Next() {
		var sid string
		err := rows.Scan(&sid)
		if err != nil {
			return nil, err
		}
		subs = append(subs, sid)
	}
	return subs, nil
}

func (store *SQLSessions) GetSubscriptions(res Resource) ([]string, error) {
	switch res.Kind {
	case "note":
		return store.subsByQuery("SELECT sid FROM sessions WHERE uid in (SELECT uid FROM noterefs WHERE nid = ?)", res.ID)
	case "folio":
		return store.subsByQuery("SELECT sid FROM sessions WHERE uid = ?", res.ID)
	case "profile":
		subs, err := store.subsByQuery("SELECT sid FROM sessions WHERE uid = ?", res.ID)
		if err != nil {
			return nil, err
		}
		contacts, err := store.subsByQuery("SELECT sid FROM sessions WHERE uid IN (SELECT uid FROM contacts WHERE contact_uid = ?)", res.ID)
		if err != nil {
			return nil, err
		}
		subs = append(subs, contacts...)
		return subs, nil
	}
	return []string{}, nil
}
