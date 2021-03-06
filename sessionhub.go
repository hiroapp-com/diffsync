package diffsync

import (
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"
)

type SessionHub struct {
	inbox       chan Event
	runner_done chan string
	active      map[string]chan Event
	backend     SessionBackend
	stopch      chan struct{}
	shutdown    chan struct{}
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
		shutdown:    make(chan struct{}),
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
		case event := <-hub.inbox:
			hub.logEvent(event)
			log.Println(hub.toSession(event))
		case <-hub.shutdown:
			return
		}
	}
}

func (hub *SessionHub) Stop() {
	log.Println("sessionhub: stop requested")
	// first shut hub inbox
	close(hub.shutdown)
	hub.wg.Wait()
	log.Println("sessionhub: stopped.")
}

func (hub *SessionHub) handleInvalidSession(err error, event Event) {
	if e, ok := err.(ErrInvalidSession); ok {
		// only notify client if it exists and is of same SID this event is addressed to
		if event.ctx.sid == event.SID && event.ctx.Client != nil {
			log.Println("clientpush session-terminated")
			event.ctx.Client.Handle(Event{SID: event.SID,
				Tag:    event.Tag,
				Name:   event.Name,
				Res:    event.Res,
				Remark: &Remark{Level: "fatal", Slug: e.Slug(), Data: map[string]string{"err-code": strconv.Itoa(int(e))}},
			})
		}
	} else {
		event.ctx.LogError(fmt.Errorf("error retrieving session `%s`: %s", event.SID, err))
	}
}

func (hub *SessionHub) toSession(event Event) error {
	// if session has an active runner, get its inbox
	inbox, ok := hub.active[event.SID]
	if !ok {
		// no active runner found
		// fetch session from sessionstore
		inbox = make(chan Event, 32)
		session, err := hub.backend.Get(event.SID)
		if err != nil {
			go hub.handleInvalidSession(err, event)
			return err
		}
		// spin up runner for session
		hub.wg.Add(1)
		go checkInbox(inbox, session, hub)
		hub.active[event.SID] = inbox
	}
	inbox <- event
	return nil
}

func checkInbox(inbox <-chan Event, session *Session, hub *SessionHub) {
	defer func(sid, uid string, h *SessionHub) {
		if e := recover(); e != nil {
			(Context{uid: uid}).LogCritical(fmt.Errorf("runtime panic: %v", e))
			hub.runner_done <- session.sid
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
	unsavedChanges := false
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
			unsavedChanges = true
		case <-hub.stopch:
			log.Printf("session[%s]: stop requested", session.sid[:6])
			break CheckInbox
		case <-idleTimeout:
			// idle for too long, shut down
			log.Printf("session[%s]: no action for 5 minutes; stopping runner", session.sid[:6])
			hub.runner_done <- session.sid
		case <-saveTicker:
			// persist sessiondata periodically
			if unsavedChanges {
				hub.backend.Save(session)
				unsavedChanges = false
			}
		}
	}
	// persist session before shutting down runner
	if unsavedChanges {
		hub.backend.Save(session)
	}
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
