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

func (rr ResourceRegistry) Add(res Resource) {
	if _, ok := rr[res.Kind]; !ok {
		rr[res.Kind] = make(map[string]bool)
	}
	rr[res.Kind][res.ID] = true
}

func (rr ResourceRegistry) Tainted() []Resource {
	tainted := make([]Resource, 0, len(rr))
	for kind, idmaps := range rr {
		for id, _ := range idmaps {
			tainted = append(tainted, Resource{Kind: kind, ID: id})
		}
	}
	return tainted
}

func (rr ResourceRegistry) Remove(res Resource) {
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
	log.Printf("session[%s]: handling %s event\n", sess.id, event.Name)
	if event.client != nil {
		log.Printf("session[%s]: setting upstream client chan\n", sess.id)
		sess.client = event.client
	}
	switch event.Name {
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
	case "client-gone":
		sess.handle_gone(event)
	default:
		sess.handle_notimplemented(event)
	}
	sess.flush()
}

func (sess *Session) handle_session_create(event Event) {
	event.Session = sess
	sess.push_client(event)
}

func (sess *Session) handle_notimplemented(event Event) {
	return
}

func (sess *Session) handle_gone(event Event) {
	log.Printf("session[%s]: client gone\n", sess.id)
	if sess.client != nil {
		close(sess.client)
		sess.client = nil
	}
}

func (sess *Session) flush() {
	// iterate over reset-resources and tainted resources and send syncs to client (if any)
	if sess.client == nil {
		log.Printf("session[%s]: flush requested, but client offline\n", sess.id)
		return
	}
	for _, res := range sess.tainted.Tainted() {
		log.Printf("session[%s]: flushin' tainted resource: %s\n", sess.id, res)
		store, ok := sess.stores[res.Kind]
		if !ok {
			log.Println("received illegal resource kind:", res.Kind)
			return
		}
		shadow, ok := sess.shadows[res.StringID()]
		if !ok {
			log.Println("shadow not found, cannot sync", res, sess.shadows)
			return

		}
		shadow.UpdatePending(store)
		sess.tainted.Remove(res.CloneEmpty())
		// TODO(flo) proper tag handling
		event := Event{Name: "res-sync", Tag: "srv01", SID: sess.id, Res: res.CloneEmpty(), Changes: shadow.pending}
		sess.taglib[res.StringID()] = event.Tag
		if !sess.push_client(event) {
			// client went offline, stop for now
			log.Printf("session[%s]: client went offline during flush. aborting", sess.id)
			return
		}
	}
	// todo: check other pending send
	return
}

func (sess *Session) handle_ehlo(event Event) {
	log.Printf("session[%s]: received client-ehlo. saved new client and flushing changes", sess.id)
	return
}

func (sess *Session) push_client(event Event) bool {
	select {
	case sess.client <- event:
		return true
	default:
	}
	// if client cannot read events, we assume he's offline
	if sess.client != nil {
		close(sess.client)
		sess.client = nil
	}
	return false
}

func (sess *Session) handle_sync(event Event) {
	// this can assume a weak promise that a client is currently connected
	// because res-sync *only* arrive on a client-request
	// still one *cannot* expect to have a receiving client (just in the middle
	// of the sync the client might have disconnected). hence, don't forget to
	// cover that edge-case
	if event.Res.ID == "" || event.Res.Kind == "" {
		// TODO(flo) data validation should happen in adapter/connection layer
		// eeeek, log and or respond that data was malformed
		// for now just discard
		log.Printf("session[%s]: malformed data; res missing\n", sess.id)
		return
	}
	if event.Changes == nil {
		log.Printf("session[%s]: malformed data; changes missing\n", sess.id)
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
	shadow, ok := sess.shadows[event.Res.StringID()]
	if !ok {
		log.Println("shadow not found, cannot sync", event.Res, sess.shadows)
		return

	}
	store, ok := sess.stores[shadow.res.Kind]
	if !ok {
		log.Println("received illegal resource kind:", shadow.res.Kind)
		return
	}
	for _, edit := range event.Changes {
		if changed, err := shadow.SyncIncoming(edit, store); err != nil {
			log.Println("ERRR sync", changed, err) //todo error handling! do we need 'changed' here?
		}
	}
	// cleanup tag
	defer delete(sess.taglib, event.Res.StringID())
	// check event-tag
	if mytag, ok := sess.taglib[event.Res.StringID()]; ok && mytag == event.Tag {
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
	event.Changes = shadow.pending
	if !sess.push_client(event) {
		// edge-case happened: client sent request and disconnected before we
		// could response. set tainted state for resource.
		log.Printf("session[%s]: client went offline during sync. taint resource (%s) for later flush", sess.id, event.Res.StringID())
		sess.tainted.Add(event.Res.CloneEmpty())
	}
	// note: the following should probably already happen at the resource
	// store layer (i.e. sending taint packets with patch.origin_sid as event.sid
	// property.
	// now tell the notifier, that the resource was tainted along
	// with this sessionid as the origin value in the res-taint event
	// this means, the current session will not receive the event (being
	// the origin), but we already updated the tainted registry above
	// on the next client-message the change will be flushed

	//notify <- Event{Name: "res-taint", SID: sess.id, Res: &event.Res.CloneEmpty()}
	return
}

func (sess *Session) handle_taint(event Event) {
	log.Printf("session[%s]: handling taint event for %s, all tainted: %s", sess.id, event.Res, sess.tainted)
	sess.tainted.Add(event.Res.CloneEmpty())
	log.Printf("session[%s]:  all tainted: %s", sess.id, sess.tainted)
}
func (sess *Session) handle_reset(event Event) {
	sess.reset.Add(event.Res.CloneEmpty())
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

func (session *Session) String() string {
	s, _ := json.MarshalIndent(session, "", "  ")
	return string(s)
}
