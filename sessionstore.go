package diffsync

import (
	"fmt"
	"log"
	"sync"
)

const (
	SESSION_NOTEXIST = iota
	SESSION_EXPIRED
	SESSION_REVOKED
)

type SessionBackend interface {
	Get(string) (*Session, error)
	//Create(*Session) error
	Save(*Session) error
	Delete(string) error
	Release(*Session)
}

type InvalidSessionId struct {
	sid    string
	reason int
}

func (err InvalidSessionId) Error() string {
	return fmt.Sprintf("invalid session-id %v", err)
}

type HiroMemSessions struct {
	db       map[string]*Session
	sessbuff chan *Session
	stores   map[string]*Store
	sync.RWMutex
}

func NewHiroMemSessions(stores map[string]*Store) *HiroMemSessions {
	return &HiroMemSessions{
		db:       make(map[string]*Session),
		sessbuff: make(chan *Session, 256),
		stores:   stores,
	}
}

func (mem *HiroMemSessions) allocate_session() *Session {
	var sess *Session
	select {
	case sess = <-mem.sessbuff:
		log.Printf("Reusing session-pointer: %v", sess)
	default:
		sess = new(Session)
	}
	return sess
}

func (mem *HiroMemSessions) Get(sid string) (*Session, error) {
	// the
	//note: leaky bucket would be nice for buffer-reuse
	mem.RLock()
	defer mem.RUnlock()
	var ok bool
	session := mem.allocate_session()
	session, ok = mem.db[sid]
	if !ok {
		mem.Release(session)
		return nil, InvalidSessionId{sid, SESSION_NOTEXIST}
	}
	return session, nil
}

func (mem *HiroMemSessions) Save(session *Session) error {
	// is an upsert, needs doc
	mem.Lock()
	defer mem.Unlock()
	log.Printf("saving session `%s`, %v", session.id, *session)
	stored := *session
	mem.db[session.id] = &stored
	return nil
}

func (mem *HiroMemSessions) Delete(sid string) error {
	mem.Lock()
	defer mem.Unlock()
	delete(mem.db, sid)
	return nil
}

func (mem *HiroMemSessions) Release(sess *Session) {
	select {
	case mem.sessbuff <- sess:
	default:
	}
}
