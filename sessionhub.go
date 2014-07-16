package diffsync

import (
	"fmt"
	"log"
	"time"
)

type SessionHub struct {
	inbox       chan Event
	runner_done chan string
	active      map[string]chan<- Event
	backend     SessionBackend
}

type ResponseTimeoutErr struct {
	sid string
}

func (err ResponseTimeoutErr) Error() string {
	return fmt.Sprintf("response to sessions timed out. sid: `%s`", err.sid)
}

func NewSessionHub(backend SessionBackend) *SessionHub {
	return &SessionHub{
		inbox:       make(chan Event),
		runner_done: make(chan string),
		active:      map[string]chan<- Event{},
		backend:     backend,
	}
}

func (hub *SessionHub) Inbox() chan<- Event {
	return hub.inbox
}

func (hub *SessionHub) logEvent(event Event) {
	//TODO(flo) write event to some persistent datastore
	// in case of a server crash or restart, this log will
	// be used to replay any unhandled events.
	log.Printf("event-log: received %v\n", event)
}

func (hub *SessionHub) route(sid string, event Event) error {
	inbox, ok := hub.active[sid]
	if !ok {
		// if not already running, load from datastore and spawn runner
		newinbox := make(chan Event, 32)
		// session will run until
		go func() {
			defer func(sid string) {
				//signal shuwdown of runner to hub
				hub.runner_done <- sid
			}(sid)
			log.Printf("session[%s]: starting up runner\n", sid)
			session, err := hub.backend.Get(sid)
			if err != nil {
				// in that case a stupid race-condition might occure: if the runner
				// aborts before routing the event to the runners-inbox, the write will
				// block and the route() call hang.
				// we work around this by using a buffered channel, thus the initial
				// event-send whithin this routine will always return
				// If this race-condition hits, we will lose this packet to nirvana
				// (channel gets closed and gc'd when routine reports it shuts down to
				// hub
				log.Printf("session[%s]: could not retrieve sessiondata. aborting.\n", sid)
				return
			}
			for event := range newinbox {
				session.Handle(event)
				// saving after every event might be a bit aggressive, implement self resetting timers
			}
			log.Printf("session[%s]: inbox shut down. persisting session and leaving event loop\n", sid)
			hub.backend.Save(session)
		}()
		hub.active[sid] = newinbox
		inbox = newinbox
	}
	log.Println("sessionhub: route event to session", event)
	// TODO(flo) handle panic from chan send in case the inbox shut down in the meanwhile!
	inbox <- event
	return nil
}

func (hub *SessionHub) Snapshot(sid string) (*Session, error) {
	//TODO(flo) in its inbox-loop, sessionhub should intervene if there is
	// no active session-runner. in this case, it should not
	// spawn one up, but instead just load the data and return it
	sessResponse := make(chan Event, 1)
	hub.Inbox() <- Event{Name: "snapshot", SID: sid, client: sessResponse}
	select {
	case event := <-sessResponse:
		//response!
		if event.Session == nil {
			return nil, SessionIDInvalidErr{sid}
		}
		return event.Session, nil
	case <-time.After(3 * time.Second):
		// request timed out. we'll ignore the old session alltogether
		// TBD should we fail hard here, so old anon session data never gets lost (because client will retry)?
		log.Printf("token: could not fetch session data for `%s`. request to hub timed out. ignoring old sessiondata and continue. ", sid)
		return nil, ResponseTimeoutErr{sid}
	}
}

func (hub *SessionHub) cleanup_runner(sid string) {
	if inbox, ok := hub.active[sid]; ok {
		log.Println("sessionhub: cleaning up runner for sid", sid)
		delete(hub.active, sid)
		close(inbox)
	}
}

func (hub *SessionHub) Run() {
	// spawn the hubrunner
	defer func() {
		for sid := range hub.active {
			log.Printf("sessionhub: shutting down runner for %s \n", sid)
			hub.cleanup_runner(sid)
		}
	}()
	log.Println("sessionhub: entering main loop")
	for {
		select {
		case sid := <-hub.runner_done:
			log.Printf("sessionhub: sess-runner signaled shutdown %s\n", sid)
			// make sure channel is closed and remove chan from active-cache
			hub.cleanup_runner(sid)
		case event, ok := <-hub.inbox:
			if !ok {
				//inbox closed, shutdown requested
				for sid := range hub.active {
					close(hub.active[sid])
				}
				return
			}
			hub.logEvent(event)
			hub.route(event.SID, event)
		}
	}
}
