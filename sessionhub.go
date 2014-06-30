package diffsync

import (
	"log"
)

type SessionHub struct {
	inbox       chan Event
	runner_done chan string
	active      map[string]chan<- Event
	backend     SessionBackend
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
			}
			log.Println("sessionid: shutting down inbox runner for sid ", sid)
		}()
		hub.active[sid] = newinbox
		inbox = newinbox
	}
	log.Println("sessionhub: route event to session", event)
	// TODO(flo) handle panic from chan send in case the inbox shut down in the meanwhile!
	inbox <- event
	return nil
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
				return
			}
			hub.logEvent(event)
			hub.route(event.SID, event)
		}
	}
}
