package diffsync

import (
    "log"
)

type SessionStore interface {
    Get(string) (*Session, error)
    Create(string) (*Session, error)
    Kill(string) error
}

type SessionHub struct {
   inbox chan Event
   runner_done chan string
   active map[string]chan<- Event
   store SessionStore
}

func NewSessionHub(store SessionStore) SessionHub {
    return SessionHub{make(chan Event), make(chan string), map[string]chan<-Event{},  store}
}

func (hub *SessionHub) route(sid string, event Event) error {
    inbox, ok := hub.active[sid]
    if !ok {
        // if not already running, load from datastore and spawn runner
        session, err := hub.store.Get(sid)
        if err != nil {
            return err 
        }
        newinbox := make(chan Event, 32)
        // session will run until 
        go func() {
            defer func(sid string) {
                //signal shuwdown of runner to hub
                hub.runner_done <-  sid
            }(sid)
            log.Println(sid, "spawning up new inbox")
            for  event := range newinbox {
                session.Handle(event)
            }
            log.Println(sid, "shutting down inbox")
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
        for sid, _ := range hub.active {
            log.Println("shutting down session-handler", sid)
            hub.cleanup_runner(sid)
        }
    }()
    for {
        select {
        case sid := <-hub.runner_done:
            // make sure channel is closed and remove chan from active-cache
            hub.cleanup_runner(sid)
        case event, ok := <-hub.inbox:
            if !ok {
                //inbox closed, shutdown requested
                return 
            }
            switch event.name {
            case "session-create":
                //handle session-create with token
                // probably we can pass the session-create event onto the newly created Session so it can repond with its part
                // e.g. send up the new session-workspace
            case "state-sync":
            case "state-reset":
            case "state-taint":
            case "client-ehlo":
            case "flush":
                hub.route(event.sid, event)
                //TODO catch write to 
        }
            }
    }
}

