package diffsync

import (
	"fmt"
	"log"
	"sync"
	"time"
)

type SessionHub struct {
	inbox       chan Event
	runner_done chan string
	active      map[string]chan Event
	backend     SessionBackend
	stopch      chan struct{}
	wg          sync.WaitGroup
}

type InvalidEventError struct{}

func (err InvalidEventError) Error() string {
	return "invalid event"
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
		runner_done: make(chan string, 64),
		active:      map[string]chan Event{},
		backend:     backend,
		stopch:      make(chan struct{}),
		wg:          sync.WaitGroup{},
	}
}

func (hub *SessionHub) Handle(event Event) error {
	if event.SID != "" {
		hub.inbox <- event
		return nil
	}
	if event.UID != "" {
		// forward to user's sessions
		ss, err := hub.backend.SessionsOfUser(event.UID)
		if err != nil {
			return err
		}
		for _, sid := range ss {
			event.SID = sid
			event.UID = ""
			if err = hub.Handle(event); err != nil {
				return err
			}
		}
		return nil
	}
	if event.Res != (Resource{}) {
		// forward to everyone interested in given resource!
		subs, err := hub.backend.GetSubscriptions(event.Res)
		if err != nil {
			return err
		}
		for uid, res := range subs {
			event.UID = uid
			event.Res = res
			if err = hub.Handle(event); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("no route matched, event could not be routed/delivered: %s", event)
}

func (hub *SessionHub) Snapshot(sid string, ctx Context) (*Session, error) {
	resp := make(chan Event, 1)
	ctx.Client = FuncHandler{func(event Event) error {
		resp <- event
		return nil
	}}
	err := hub.Handle(Event{Name: "snapshot", SID: sid, ctx: ctx})
	if err != nil {
		return nil, err
	}
	select {
	case event := <-resp:
		//response!
		if event.Session == nil {
			return nil, SessionIDInvalidErr{sid}
		}
		return event.Session, nil
	case <-time.After(5 * time.Second):
		// request timed out. we'll ignore the old session alltogether
		// TBD should we fail hard here, so old anon session data never gets lost (because client will retry)?
		log.Printf("token: could not fetch session data for `%s`. request to hub timed out. ignoring old sessiondata and continue. ", sid)
		return nil, ResponseTimeoutErr{sid}
	}
}

func (hub *SessionHub) Run() {
	// spawn the hubrunner
	log.Println("sessionhub: entering main loop")
	defer close(hub.stopch)
	for {
		select {
		case sid := <-hub.runner_done:
			log.Printf("sessionhub: sess-runner signaled shutdown %s\n", sid)
			// close channel and remove from active runners
			hub.cleanup_runner(sid)
		case event, ok := <-hub.inbox:
			if !ok {
				//inbox closed, shutdown requested
				return
			}
			hub.logEvent(event)
			hub.toSession(event.SID, event)
		}
	}
}

func (hub *SessionHub) Stop() {
	log.Println("sessionhub: stop requested")
	// first shut hub inbox
	tmp := hub.inbox
	hub.inbox = nil
	close(tmp)
	hub.wg.Wait()
	log.Println("sessionhub: stopped.")
}

func (hub *SessionHub) toSession(sid string, event Event) error {
	// if session has an active runner, get its inbox
	inbox, ok := hub.active[sid]
	if !ok {
		// no active runner found
		// fetch session from sessionstore
		inbox = make(chan Event, 32)
		session, err := hub.backend.Get(sid)
		if err != nil {
			return err
		}
		// spin up runner for session
		hub.wg.Add(1)
		go checkInbox(inbox, session, hub)
		hub.active[sid] = inbox
	}
	inbox <- event
	return nil
}

func checkInbox(inbox <-chan Event, session *Session, hub *SessionHub) {
	defer func(sid, uid string, h *SessionHub) {
		if e := recover(); e != nil {
			(Context{uid: uid}).LogCritical(fmt.Errorf("runtime panic: %v", e))
		}
		//signal shuwdown of runner to hub
		h.wg.Done()
		log.Printf("session[%s]: runner stopped.", sid[:6])
	}(session.sid, session.uid, hub)
	if session == nil {
		panic("NILSESSION")
	}
	if session.sid == "" {
		log.Println(session)
		panic("EMPTY SID")
	}

	log.Printf("(sessionhub starting runner for %s)", session.sid)
	saveTicker := time.Tick(1 * time.Minute)
	// event loop runs is being executed for the
	// whole lifetime of this runner.
	idleTimeout := time.After(5 * time.Minute)
CheckInbox:
	for {
		select {
		case event, ok := <-inbox:
			if !ok {
				log.Printf("session[%s]: inbox shut down; stopping runner", session.sid[:6])
				break CheckInbox
			}
			session.Handle(event)
			idleTimeout = time.After(5 * time.Minute)
		case <-hub.stopch:
			log.Printf("session[%s]: stop requested", session.sid[:6])
			break CheckInbox
		case <-idleTimeout:
			// idle for too long, shut down
			log.Printf("session[%s]: no action for 5 minutes; stopping runner", session.sid[:6])
			hub.runner_done <- session.sid
		case <-saveTicker:
			// persist sessiondata periodically
			hub.backend.Save(session)
		}
	}
	// persist session before shutting down runner
	hub.backend.Save(session)
}

func (hub *SessionHub) logEvent(event Event) {
	//TODO(flo) write event to some persistent datastore
	// in case of a server crash or restart, this log will
	// be used to replay any unhandled events.
	log.Printf("event-log: received %s\n", event)
}

func (hub *SessionHub) cleanup_runner(sid string) {
	if inbox, ok := hub.active[sid]; ok {
		delete(hub.active, sid)
		close(inbox)
	}
}
