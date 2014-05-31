package diffsync

import (
	"log"
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
	TokenConsumer
	MessageAdapter
}

func NewConn(hub chan<- Event, consumer TokenConsumer, adapter MessageAdapter) *Conn {
	log.Printf("conn: spawning new connection \n")
	return &Conn{sessionhub: hub, to_client: make(chan Event, 32), TokenConsumer: consumer, MessageAdapter: adapter}
}

func (conn *Conn) Close() {
	log.Printf("conn[%p]: shutting down, stopping listening on channel\n", conn)
	conn.to_client = nil
	conn.sessionhub <- Event{Name: "client-gone", SID: conn.sid}
}

func (conn *Conn) ToClient() <-chan Event {
	return conn.to_client
}

func validToken(key string) bool {
	return true
}

func validSID(key string) bool {
	return true
}

func (conn *Conn) ClientEvent(event Event) {
	if event.Name == "session-create" {
		log.Printf("conn[%p]: received `session-create` with token: `%s` and sid `%s`\n", conn, event.Token, event.SID)
		if !validToken(event.Token) || !validSID(event.SID) {
			//malformed data
			// response with error response on to_client
			log.Printf("conn[%p]: malformed data, abort.\n", conn)
			return
		}
		var err error
		event.SID, err = conn.TokenConsumer.Consume(event.Token, event.SID)
		if err != nil {
			log.Printf("conn[%p]: token cannot be consumed, aborting session-create\n", conn)
			//todo tell to_client about the error
			return
		}
		conn.sid = event.SID
	}
	event.client = conn.to_client
	conn.sessionhub <- event
}
