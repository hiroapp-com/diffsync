package diffsync

import (
	"log"
	"sync"
	"time"

	"database/sql"
)

const SessionLifetime = 24 * time.Hour * 60

type SQLSessions struct {
	db       *sql.DB
	sessbuff chan *Session
	uidCache map[string]string
	uidLock  sync.Mutex
}

func NewSQLSessions(db *sql.DB) *SQLSessions {
	return &SQLSessions{
		db:       db,
		sessbuff: make(chan *Session, 256),
		uidCache: map[string]string{},
	}
}

func (store *SQLSessions) Get(sid string) (*Session, error) {
	session := NewSession(sid, "")
	created := time.Time{}
	status := ""
	err := store.db.QueryRow("SELECT data, created_at, status FROM sessions where sid = $1", sid).Scan(session, &created, &status)
	if err == sql.ErrNoRows {
		return nil, ErrInvalidSession(SessionNotfound)
	} else if err != nil {
		return nil, err
	}
	if time.Now().Sub(created) > SessionLifetime {
		return nil, ErrInvalidSession(SessionExpired)
	}
	if status == "terminated" {
		return nil, ErrInvalidSession(SessionTerminated)
	}
	return session, nil
}

func (store *SQLSessions) Save(session *Session) error {
	// is an upsert, needs doc
	log.Printf("sessionbackend: saving %s", session.sid)
	res, err := store.db.Exec("UPDATE sessions SET uid = $1, data = cast($2 as text), saved_at = now() WHERE sid = $3", session.uid, session, session.sid)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows > 0 {
		// updated, all fine
		return nil
	}
	// nothing was updated, need to create session
	_, err = store.db.Exec("INSERT INTO sessions (sid, uid, data, saved_at) VALUES ($1, $2, $3, now())", session.sid, session.uid, session)
	return err
}

func (store *SQLSessions) GetUID(sid string) (string, error) {
	store.uidLock.Lock()
	uid, ok := store.uidCache[sid]
	defer store.uidLock.Unlock()
	if !ok {
		err := store.db.QueryRow("SELECT uid FROM sessions where sid = $1", sid).Scan(&uid)
		if err == sql.ErrNoRows {
			return "", ErrInvalidSession(SessionNotfound)
		} else if err != nil {
			return "", err
		}
		store.uidCache[sid] = uid
	}
	return uid, nil
}

func (store *SQLSessions) SessionsOfUser(uid string) ([]string, error) {
	sids := []string{}
	rows, err := store.db.Query("SELECT sid FROM sessions WHERE uid = $1 AND status = 'active' AND created_at > $2", uid, time.Now().Add((-1)*SessionLifetime))
	if err != nil {
		return sids, err
	}
	defer rows.Close()
	for rows.Next() {
		var sid string
		if err = rows.Scan(&sid); err != nil {
			return sids, err
		}
		sids = append(sids, sid)
	}
	return sids, nil
}

func (store *SQLSessions) GetSubscriptions(res Resource) (map[string]Resource, error) {
	switch res.Kind {
	case "note":
		// get all sessions of all users who have a noteref for this note
		return store.subsByQuery(res, "SELECT uid FROM noterefs WHERE nid = $1", res.ID)
	case "folio":
		// get all sessions of this folio's user
		return map[string]Resource{res.ID: res}, nil
	case "profile":
		// first get all session of this profile's user
		return store.subsByQuery(Resource{Kind: "profile"}, `SELECT uid 
										                      FROM users 
									                          WHERE uid = $1 
										                        OR uid in (SELECT uid FROM contacts WHERE contact_uid = $1)
										                        OR uid in (SELECT nr.uid 
										                     		      FROM noterefs as nr
										                     				 LEFT OUTER JOIN noterefs as nr2
										                     				  ON nr.nid = nr2.nid AND nr2.uid = $1
										                     			  WHERE nr.uid <> $1 AND nr2.uid is not null)`, res.ID)
	}
	return map[string]Resource{}, nil
}

func (store *SQLSessions) subsByQuery(res Resource, qry string, args ...interface{}) (map[string]Resource, error) {
	rows, err := store.db.Query(qry, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	subs := map[string]Resource{}
	resetResID := (res.ID == "")
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		if resetResID {
			res.ID = uid
		}
		subs[uid] = Resource{Kind: res.Kind, ID: res.ID}
	}
	return subs, nil
}
