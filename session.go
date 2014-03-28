package diffsync

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
)

var (
	_ = log.Print
)

type Auther interface {
	Grant(string, string, *Resource)
}

type ResourceRegistry map[string]map[string]bool

func (rr ResourceRegistry) Add(res *Resource) {
	if _, ok := rr[res.Kind]; !ok {
		rr[res.Kind] = make(map[string]bool)
	}
	rr[res.Kind][res.ID] = true
}

func (rr ResourceRegistry) Remove(res *Resource) {
	if _, ok := rr[res.Kind]; ok {
		delete(rr[res.Kind], res.ID)
		if len(rr[res.Kind]) == 0 {
			delete(rr, res.Kind)
		}
	}
}

type Session struct {
	id      string
	uid     string
	taglib  map[string]string
	tainted ResourceRegistry
	reset   ResourceRegistry
	client  chan<- Event
	shadows map[string]*Shadow
	stores  map[string]*Store
}

func sid_generate() string {
	uuid := make([]byte, 16)
	if n, err := rand.Read(uuid); err != nil || n != len(uuid) {
		panic(err)
	}
	// RFC 4122
	uuid[8] = 0x80 // variant bits
	uuid[4] = 0x40 // v4
	return hex.EncodeToString(uuid)
}

func NewSession(uid string) *Session {
	return &Session{
		id:      sid_generate(),
		uid:     uid,
		taglib:  make(map[string]string),
		tainted: make(ResourceRegistry),
		reset:   make(ResourceRegistry),
		client:  nil,
		shadows: make(map[string]*Shadow),
	}
}

func (sess *Session) diff_resources(check []Resource) []Resource {
	news := make([]Resource, 0, len(check))
	for _, res := range check {
		if _, exists := sess.shadows[res.StringID()]; !exists {
			news = append(news, res)
		}
	}
	return news
}
func (sess *Session) Handle(event Event) {
	if event.client != nil {
		sess.client = event.client
	}
	switch event.name {
	case "session-create":
		sess.handle_session_create(event)
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

func (sess *Session) handle_session_create(event Event) {
	log.Printf("pushing event %v", event)
	event.data = SessionData{"", sess}
	log.Printf("changed event %v", event)
	sess.push_client(event)
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
	shadow, ok := sess.shadows[data.res.StringID()]
	if !ok {
		log.Println("shadow not found, cannot sync", data.res, sess.shadows)
		return

	}
	store, ok := sess.stores[shadow.res.kind]
	if !ok {
		log.Println("received illegal resource kind:", shadow.res.kind)
		return
	}
	for _, edit := range data.changes {
		if changed, err := shadow.SyncIncoming(edit, store); err != nil {
			log.Println("ERRR sync", changed, err) //todo error handling! do we need 'changed' here?
		}
	}
	// cleanup tag
	defer delete(sess.taglib, data.res.StringID())
	// check event-tag
	if mytag, ok := sess.taglib[data.res.StringID()]; ok && mytag == event.tag {
		//received a response to a server-side-sync(sss) cycle
		// we're all done!
		// note this relies on the fact that during sync-incoming, appropriate
		// res-taint events have been sent to nofity and we don't have to care
		// about change-propagation anymore at this point and can leave
		return
	}
	// Preparing and sending out the changes for the response to the css

	// calculate changes and add them to pending and incease our SV
	shadow.UpdatePending(store)
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

type SessionData struct {
	token   string
	session *Session
}

func (s SessionData) MarshalJSON() ([]byte, error) {
	//folio := Resource{}
	//contacts := Resource{}
	notes := make(map[string]*Resource)
	//meta := make(map[string]Resource)

	for _, shadow := range s.session.shadows {
		switch shadow.res.kind {
		//   case "folio":
		//       folio = shadow.res
		//   case "contacts":
		//       contacts = shadow.res
		case "note":
			notes[shadow.res.id] = &shadow.res
			//        case "meta":
			//meta[shadow.res.id] = shadow.res
		default:
		}

	}
	return json.Marshal(map[string]interface{}{
		"sid":   s.session.id,
		"token": s.token,
		"uid":   s.session.uid,
		//"folio":  folio,
		//"contacts": contacts,
		"notes": notes,
		//"meta": meta,
	})
}
