package diffsync


import (
    "log"
)

var (
    _ = log.Print
)


type Session struct {
    id string
    acl Auther
    tainted ResourceRegistry
    reset ResourceRegistry
    client chan<- Event
    shadows map[string]Shadow 
}

func (sess *Session) Handle(event Event) {
    switch event.name {
    case "res-taint":
        sess.handle_taint(event)
    case "shadow-reset":
        sess.handle_reset(event)
    case "res-sync":
        sess.handle_sync(event)
    case "client-ehlo": 
        sess.handle_ehlo(event)
    default:
        sess.handle_notimplemented(event)
    }
}

func (sess *Session) handle_notimplemented(event Event) {
    return 
}

func (sess *Session) flush(event Event) {
    // iterate over reset-resources and tainted resources and send syncs to client (if any)
    return 
}

func (sess *Session) handle_ehlo(event Event) {
    sess.client = event.client
    return
}


func (sess *Session) handle_sync(event Event) {
    // get shadow for sync and use it for diffsync
    // flow (incl version checking etc)
    return
}

func (sess *Session) handle_taint(event Event) {
    //sess.tainted_states.Add(data.res)
    return 
}
func (sess *Session) handle_reset(event Event) {
    //state, ok := (event.data).(State)
    //if !ok {
    //    // malformed, pass on; how should we handle this case?
    //    continue
    //}
    //if event.tag == "" && sess.client == nil {
    //   sess.reset_states.Add(state.res)
    //}
    //state = statestore.New(sess.id, res, id)
    //if event.tag == "" {
    //    event.tag = taglib.NewTag(state)
    //}
    // check other tagging stuff
}

