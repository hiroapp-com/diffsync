package diffsync

import (
	"fmt"
	"log"
	"math/rand"
	"time"
)

type SessionHub struct {
	inbox       chan Event
	runner_done chan string
	active      map[string]chan Event
	cache       map[string]*Session
	cacheIndex  []string
	backend     SessionBackend
}

type subscription struct {
	sid string
	uid string
	res Resource
}

type HubTimeoutError struct{}
type InvalidEventError struct{}

func (err HubTimeoutError) Error() string {
	return "Routing an event timed out. sesisonhub not responding"
}

func (err InvalidEventError) Error() string {
	return "invalid event"
}

type ResponseTimeoutErr struct {
	sid string
}

func (err ResponseTimeoutErr) Error() string {
	return fmt.Sprintf("response to sessions timed out. sid: `%s`", err.sid)
}

type RouteHandler struct {
	hub *SessionHub
}
type BroadcastHandler struct {
	hub *SessionHub
}

func (handler RouteHandler) Handle(event Event) error {
	return handler.hub.Route(event)
}
func (handler BroadcastHandler) Handle(event Event) error {
	return handler.hub.Broadcast(event)
}

func NewSessionHub(backend SessionBackend) *SessionHub {
	return &SessionHub{
		inbox:       make(chan Event),
		runner_done: make(chan string, 64),
		active:      map[string]chan Event{},
		cache:       map[string]*Session{},
		cacheIndex:  make([]string, 0, 1024),
		backend:     backend,
	}
}

func (hub *SessionHub) Route(event Event) error {
	select {
	case hub.inbox <- event:
		return nil
	case <-time.After(5 * time.Second):
		return HubTimeoutError{}
	}
}

func (hub *SessionHub) Broadcast(event Event) error {
	log.Println("sessionhub(bcast): got ", event)
	// filter out broadcast'able events
	switch event.Name {
	case "res-taint", "res-reset":
	default:
		return InvalidEventError{}
	}
	if event.SID != "" {
		// addressed directly to a certain session, pipe into Router
		// TODO(flo) is this still necessary? will after the refactor not every holder
		//   a (former) notifylistener, also have Route() at hand?
		return hub.Route(event)
	}
	subs, err := hub.backend.GetSubscriptions(event.Res)
	if err != nil {
		return err
	}
	for i := range subs {
		log.Printf("sessionhub(bcast): routing to session[%s]", subs[i])
		if err = hub.Route(Event{Name: event.Name, SID: subs[i].sid, Res: subs[i].res, ctx: event.ctx}); err != nil {
			return err
		}
	}
	return nil
}

func (hub *SessionHub) Snapshot(sid string, ctx Context) (*Session, error) {
	resp := make(chan Event, 1)
	ctx.Client = FuncHandler{func(event Event) error {
		resp <- event
		return nil
	}}
	err := hub.Route(Event{Name: "snapshot", SID: sid, ctx: ctx})
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

func (hub *SessionHub) Stop() {
	tmp := hub.inbox
	hub.inbox = nil
	close(tmp)
	// TODO we should have a shutdown-signaller channel around
	// which will be shared to all shut-downable resources
	// so one close can kill all processes
}

func (hub *SessionHub) route(sid string, event Event) error {
	// if session has an active runner, get its inbox
	inbox, ok := hub.active[sid]
	if ok {
		// found active session-runner
		inbox <- event
		return nil
	}
	// no active sessionrunner available, see
	// if we have the session still in cache
	session, ok := hub.cache[sid]
	if !ok {
		// cache miss, load from backend and save to cache
		var err error
		session, err = hub.backend.Get(sid)
		if err != nil {
			return err
		}
		hub.addCache(session)
	}
	// spin up runner for cached session
	inbox = make(chan Event, 32)
	go checkInbox(inbox, session, hub)
	hub.active[sid] = inbox
	log.Println("sessionhub: route event to session", event)
	inbox <- event
	return nil
}

func checkInbox(inbox <-chan Event, session *Session, hub *SessionHub) {
	if session == nil {
		panic("NILSESSION")
	}
	defer func(sid string, done chan string) {
		//signal shuwdown of runner to hub
		done <- sid
	}(session.sid, hub.runner_done)

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
		case <-idleTimeout:
			// idle for too long, shut down
			// session will *not* be evicted from cache
			log.Printf("session[%s]: no action for 5 minutes; stopping runner", session.sid[:6])
			break CheckInbox
		case <-saveTicker:
			// persist sessiondata periodically
			hub.backend.Save(session)
		}
	}
	// persist session before shutting down runner
	hub.backend.Save(session)
}

func (hub *SessionHub) addCache(session *Session) {
	if len(hub.cacheIndex) < 1024 {
		hub.cacheIndex = append(hub.cacheIndex, session.sid)
	} else {
		idx := rand.Int63n(int64(len(hub.cacheIndex)))
		// Int63n(n) chooses from [0,n), so we can use it directly
		// in the slice-index without running into oboe
		evict := hub.cacheIndex[idx]
		hub.cache[evict] = nil
		// take the spot
		hub.cacheIndex[idx] = session.sid
	}
	hub.cache[session.sid] = session
}

func (hub *SessionHub) logEvent(event Event) {
	//TODO(flo) write event to some persistent datastore
	// in case of a server crash or restart, this log will
	// be used to replay any unhandled events.
	log.Printf("event-log: received %s\n", event)
}

func (hub *SessionHub) cleanup_runner(sid string) {
	if inbox, ok := hub.active[sid]; ok {
		log.Println("sessionhub: cleaning up runner for sid", sid)
		delete(hub.active, sid)
		close(inbox)
	}
}
