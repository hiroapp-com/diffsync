package diffsync

import (
	"log"
)

type SessionHub struct {
	inbox       chan Event
	runner_done chan string
	active      map[string]chan<- Event
	backend     SessionBackend
	stores      map[string]*Store
}

func NewSessionHub(backend SessionBackend, stores map[string]*Store) *SessionHub {
	return &SessionHub{
		inbox:       make(chan Event),
		runner_done: make(chan string),
		active:      map[string]chan<- Event{},
		backend:     backend,
		stores:      stores,
	}
}

func (hub *SessionHub) Inbox() chan<- Event {
	return hub.inbox
}

func (hub *SessionHub) route(sid string, event Event) error {
	log.Printf("routing package for sid %s", sid)
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
			log.Println(sid, "spawning up new inbox runner")
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
				log.Println(sid, "Cannot fetch Session from sessionstore. aborting runner.")
				return
			}
			session.stores = hub.stores
			for event := range newinbox {
				session.Handle(event)
			}
			log.Println(sid, "shutting down inbox runner")
		}()
		hub.active[sid] = newinbox
		inbox = newinbox
	}
	inbox <- event
	return nil
}

func (hub *SessionHub) cleanup_runner(sid string) {
	if inbox, ok := hub.active[sid]; ok {
		log.Println(sid, "stopping runner")
		delete(hub.active, sid)
		close(inbox)
	}
}

func (hub *SessionHub) Run() {
	// spawn the hubrunner
	defer func() {
		for sid := range hub.active {
			log.Println("shutting down session-handler", sid)
			hub.cleanup_runner(sid)
		}
	}()
	log.Println("spawning hub-runner...")
	for {
		select {
		case sid := <-hub.runner_done:
			log.Printf("received: runner done (%s)", sid)
			// make sure channel is closed and remove chan from active-cache
			hub.cleanup_runner(sid)
		case event, ok := <-hub.inbox:
			if !ok {
				//inbox closed, shutdown requested
				return
			}
			log.Printf("received: event (%s:%s)", event.sid, event.name)
			hub.route(event.sid, event)
		}
	}
}
