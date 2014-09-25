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
	session := NewSession(sid, "")
	err := store.db.QueryRow("SELECT data FROM sessions where sid = ?", sid).Scan(session)
	if err == sql.ErrNoRows {
		//store.Release(session)
		return nil, SessionIDInvalidErr{sid}
	} else if err != nil {
		//store.Release(session)
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
		log.Printf("Reusing session-pointer: %p", sess)
	default:
		sess = new(Session)
	}
	return sess
}

func (store *SQLSessions) subsByQuery(res Resource, qry string, args ...interface{}) ([]subscription, error) {
	rows, err := store.db.Query(qry, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	subs := []subscription{}
	resetResID := (res.ID == "")
	for rows.Next() {
		var sid, uid string
		err := rows.Scan(&sid, &uid)
		if err != nil {
			return nil, err
		}
		if resetResID {
			res.ID = uid
		}
		subs = append(subs, subscription{sid: sid, uid: uid, res: res})
	}
	return subs, nil
}

func (store *SQLSessions) GetSubscriptions(res Resource) ([]subscription, error) {
	switch res.Kind {
	case "note":
		// get all sessions of all users who have a noteref for this note
		return store.subsByQuery(res, "SELECT sid, uid FROM sessions WHERE uid in (SELECT uid FROM noterefs WHERE nid = ?)", res.ID)
	case "folio":
		// get all sessions of this folio's user
		return store.subsByQuery(res, "SELECT sid, uid FROM sessions WHERE uid = ?", res.ID)
	case "profile":
		// first get all session of this profile's user
		return store.subsByQuery(Resource{Kind: "profile"}, `SELECT sid, uid 
										                      FROM sessions 
									                          WHERE uid = ?
										                        OR uid in (SELECT uid FROM contacts WHERE contact_uid = ?)
										                        OR uid in (SELECT nr.uid 
										                     		      FROM noterefs as nr
										                     				 LEFT OUTER JOIN noterefs as nr2
										                     				  ON nr.nid = nr2.nid AND nr2.uid = ?
										                     			  WHERE nr.uid <> ? AND nr2.uid is not null)`, res.ID, res.ID, res.ID, res.ID)
	}
	return []subscription{}, nil
}
