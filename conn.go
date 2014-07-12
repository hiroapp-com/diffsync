package diffsync

import (
	"log"
	"time"
)

type MessageAdapter interface {
	MsgToEvent([]byte) (Event, error)
	EventToMsg(Event) ([]byte, error)
	Demux([]byte) ([][]byte, error)
	Mux([][]byte) ([]byte, error)
}

type Conn struct {
	sid        string
	sessionhub chan<- Event
	to_client  chan Event
	store      *Store
	TokenConsumer
	MessageAdapter
}

func NewConn(hub chan<- Event, consumer TokenConsumer, adapter MessageAdapter, store *Store) *Conn {
	log.Printf("conn: spawning new connection \n")
	return &Conn{sessionhub: hub, to_client: make(chan Event, 32), TokenConsumer: consumer, MessageAdapter: adapter, store: store}
}

func (conn *Conn) ClientEvent(event Event) {
	event.ctx = context{ts: time.Now()}
	switch event.Name {
	case "session-create":
		log.Printf("conn[%p]: received `session-create` with token: `%s`\n", conn, event.Token)
		session, err := conn.TokenConsumer.CreateSession(event.Token, event.SID, conn.store)
		if err != nil {
			log.Printf("conn[%p]: token cannot be consumed, aborting session-create. err: %s\n", conn, err)
			//todo tell to_client about the error
			return
		}
		event.ctx.uid = session.uid
		event.SID = session.sid
		conn.sid = session.sid
	case "token-consume":
		log.Printf("conn[%p]: received `token-consume` with token: `%s` and sid `%s`\n", conn, event.Token, event.SID)
		session, err := conn.TokenConsumer.Consume(event.Token, event.SID, conn.store)
		if err != nil {
			log.Printf("conn[%p]: token cannot be consumed, aborting session-create, err: %s\n", conn, err)
			//todo tell to_client about the error
			return
		}
		event.ctx.uid = session.uid
		conn.sid = session.sid
	default:
		uid, err := conn.TokenConsumer.GetUID(event.SID)
		if err != nil {
			return
		}
		event.ctx.uid = uid
		conn.sid = event.SID
	}
	event.ctx.sid = event.SID
	event.store = conn.store
	event.client = conn.to_client
	conn.sessionhub <- event
}

func (conn *Conn) Close() {
	log.Printf("conn[%p]: shutting down, stopping listening on channel\n", conn)
	conn.to_client = nil
	if conn.sid != "" {
		conn.sessionhub <- Event{Name: "client-gone", SID: conn.sid}
	}
}

func (conn *Conn) ToClient() <-chan Event {
	return conn.to_client
}
