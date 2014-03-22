package diffsync

import (
	"encoding/json"
	"log"
)

var (
	_ = log.Print
)

type Session struct {
	id      string
	uid     string
	taglib  map[string]string
	tainted ResourceRegistry
	reset   ResourceRegistry
	client  chan<- Event
	shadows map[string]*Shadow
}

func sid_generate() string {
	return "fooo"
}

func NewSession(uid string, resources []Resource) *Session {
	shadows := make(map[string]*Shadow)
	for _, res := range resources {
		shadows[res.StringId()] = NewShadow(res)
	}
	return &Session{
		id:      sid_generate(),
		uid:     uid,
		taglib:  make(map[string]string),
		tainted: make(ResourceRegistry),
		reset:   make(ResourceRegistry),
		client:  nil,
		shadows: shadows,
	}
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

func (sess *Session) push_client(event Event) bool {
	select {
	case sess.client <- event:
		return true
	default:
	}
	return false
}

func (sess *Session) handle_sync(event Event) {
	// this can assume a weak promise that a client is currently connected
	// because res-sync *only* arrive on a client-request
	// still one *cannot* expect to have a receiving client (just in the middle
	// of the sync the client might have disconnected). hence, don't forget to
	// cover that edge-case
	data, ok := event.data.(SyncData)
	if !ok {
		// eeeek, log and or respond that data was malformed
		// for now just discard
		return
	}
	// todo(ACL) check if session may access data.res
	// note: we do not check the event-tag here, because the server will
	// always ablige to a res-sync event, whether it's a response of a cycle
	// or an initiation. If the client initiates a res-sync simultaneously,
	// the server will *not* ignore the incoming request on the fact that the
	// taglib indicates a pending tag. It will ignore the tag (and its own
	// cycle) and process like a regular client-side-sync(css). (don't forget to
	// update taglib in the end
	shadow := sess.shadows[data.res.StringId()]
	for _, edit := range data.changes {
		changed, err := shadow.SyncIncoming(edit)
		log.Println(changed, err) //todo error handling! do we need 'changed' here?
	}
	// cleanup tag
	defer delete(sess.taglib, data.res.StringId())
	// check event-tag
	if mytag, ok := sess.taglib[data.res.StringId()]; ok && mytag == event.tag {
		//received a response to a server-side-sync(sss) cycle
		// we're all done!
		// note this relies on the fact that during sync-incoming, appropriate
		// res-taint events have been sent to nofity and we don't have to care
		// about change-propagation anymore at this point and can leave
		return
	}
	// Preparing and sending out the changes for the response to the css

	// calculate changes and add them to pending and incease our SV
	shadow.UpdatePending()
	data.changes = shadow.pending
	event.data = data
	if !sess.push_client(event) {
		// edge-case happened: client sent request and disconnected before we
		// could response. set tainted state for resource.
		sess.tainted.Add(&data.res)
	}
	// note: the following should probably already happen at the resource
	// store layer (i.e. sending taint packets with patch.origin_sid as event.sid
	// property.
	// now tell the notifier, that the resource was tainted along
	// with this sessionid as the origin value in the res-taint event
	// this means, the current session will not receive the event (being
	// the origin), but we already updated the tainted registry above
	// on the next client-message the change will be flushed
	notify <- Event{name: "res-taint", sid: sess.id, data: data.res.CloneEmpty()}
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

func (s *Session) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"id":      s.id,
		"uid":     s.uid,
		"taglib":  s.taglib,
		"tainted": s.tainted,
		"reset":   s.reset,
		"shadows": s.shadows,
	})
}

func (session *Session) UnmarshalJSON(from []byte) error {
	vals := make(map[string]interface{})
	json.Unmarshal(from, vals)
	*session = Session{id: vals["id"].(string),
		uid:     vals["uid"].(string),
		taglib:  vals["taglib"].(map[string]string),
		tainted: vals["tainted"].(ResourceRegistry),
		reset:   vals["reset"].(ResourceRegistry),
		shadows: vals["shadows"].(map[string]*Shadow),
	}
	return nil
}
