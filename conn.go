package diffsync

import (
	"log"
)

type Conn struct {
	sessionhub chan<- Event
	to_client  chan Event
	TokenConsumer
}

func NewConn(hub chan<- Event, consumer TokenConsumer) *Conn {
	return &Conn{hub, make(chan Event, 32), consumer}
}

func (conn *Conn) Close() {
	close(conn.to_client)
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
		log.Println("connection received `session-create` token: ", event.Token, "sid:", event.SID)
		if !validToken(event.Token) || !validSID(event.SID) {
			//malformed data
			// response with error response on to_client
			log.Println("connection: malformed data, abort. sid:", event.SID)
			return
		}
		var err error
		event.SID, err = conn.TokenConsumer.Consume(event.Token, event.SID)
		if err != nil {
			log.Println("token cannot be consumed, aborting session-create")
			//todo tell to_client about the error
			return
		}
	}
	event.client = conn.to_client
	conn.sessionhub <- event
}
