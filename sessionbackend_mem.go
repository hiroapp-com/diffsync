package diffsync

import (
	"log"
	"sync"
)

type MemSessions struct {
	db       map[string]*Session
	sessbuff chan *Session
	sync.RWMutex
}

func NewMemSessions() *MemSessions {
	return &MemSessions{
		db:       make(map[string]*Session),
		sessbuff: make(chan *Session, 256),
	}
}

func (mem *MemSessions) allocate_session() *Session {
	var sess *Session
	select {
	case sess = <-mem.sessbuff:
		log.Printf("Reusing session-pointer: %v", sess)
	default:
		sess = new(Session)
	}
	return sess
}

func (mem *MemSessions) GetUID(sid string) (string, error) {
	sess, err := mem.Get(sid)
	if err != nil {
		return "", err
	}
	return sess.uid, nil
}

func (mem *MemSessions) Get(sid string) (*Session, error) {
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

func (mem *MemSessions) Save(session *Session) error {
	// is an upsert, needs doc
	mem.Lock()
	defer mem.Unlock()
	log.Printf("saving session `%s`, %v", session.sid, *session)
	stored := *session
	mem.db[session.sid] = &stored
	return nil
}

func (mem *MemSessions) Delete(sid string) error {
	mem.Lock()
	defer mem.Unlock()
	delete(mem.db, sid)
	return nil
}

func (mem *MemSessions) Release(sess *Session) {
	select {
	case mem.sessbuff <- sess:
	default:
	}
}

func (mem *MemSessions) GetSubscriptions(ref Subscription) []Subscription {
	// SUBSCRIBE ALL THE SESSIONS
	subs := make([]Subscription, 0, len(mem.db))
	for _, sess := range mem.db {
		subs = append(subs, Subscription{sid: sess.sid, uid: sess.uid, res: ref.res.Ref()})
	}
	return subs
}
