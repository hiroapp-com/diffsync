package diffsync

import (
	"fmt"
	"log"
	"time"

	"crypto/rand"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
)

var (
	_ = log.Print
)

const (
	SESSION_NOTEXIST = iota
	SESSION_EXPIRED
	SESSION_REVOKED
)

type SessionBackend interface {
	Get(string) (*Session, error)
	GetUID(string) (string, error)
	Save(*Session) error
	Delete(string) error
	Release(*Session)
	GetSubscriptions(Resource) ([][2]string, error)
}

type Auther interface {
	Grant(context, string, Resource)
}

type ResourceRegistry map[string]map[string]bool

type SessionIDInvalidErr struct {
	sid string
}

type Tag struct {
	Ref      string    `json:"ref"`
	Val      string    `json:"val"`
	LastSent time.Time `json:"last_sent"`
}

type Session struct {
	sid     string
	uid     string
	shadows []*Shadow
	tainted []Resource
	flushes map[string]time.Time
	tags    []Tag
	client  chan<- Event
}

func (session *Session) String() string {
	s, _ := json.MarshalIndent(session, "", "  ")
	return string(s)
}

func (session *Session) Snapshot() *Session {
	snap := *session
	for i := range session.shadows {
		cpy := *session.shadows[i]
		snap.shadows[i] = &cpy
	}
	return &snap
}

func NewSession(sid, uid string) *Session {
	return &Session{
		sid:     sid,
		uid:     uid,
		shadows: []*Shadow{},
		tainted: []Resource{},
		tags:    []Tag{},
		flushes: map[string]time.Time{},
		client:  nil,
	}
}

func (sess *Session) Handle(event Event) {
	log.Printf("session[%s]: handling %s event\n", sess.sid, event.Name)
	if event.Name != "snapshot" && event.client != nil {
		log.Printf("session[%s]: setting upstream client chan\n", sess.sid)
		sess.client = event.client
	}
	switch event.Name {
	case "session-create":
		sess.handle_session_create(event)
	case "token-consume":
		sess.handle_token_consume(event)
	case "res-taint":
		sess.handle_taint(event)
	case "res-reset":
		sess.handle_reset(event)
	case "res-sync":
		sess.handle_sync(event)
	case "client-ehlo":
		sess.handle_ehlo(event)
	case "client-gone":
		sess.handle_gone(event)
	case "snapshot":
		sess.handle_snapshot(event)
	default:
		sess.handle_notimplemented(event)
	}
	sess.flush(event.store)
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
		log.Printf("session[%s]: malformed data; res missing\n", sess.sid)
		return
	}
	if event.Changes == nil {
		log.Printf("session[%s]: malformed data; changes missing\n", sess.sid)
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
	shadow, ok := sess.getShadow(event.Res)
	if !ok {
		log.Println("shadow not found, cannot sync", event.Res, sess.shadows)
		return

	}
	for _, edit := range event.Changes {
		err := shadow.SyncIncoming(edit, event.store, event.ctx)
		if err != nil {
			log.Println("ERRR sync:", err) //todo error handling! do we need 'changed' here?
		}
	}
	tag, ok := sess.getTag(shadow.res.StringRef())
	if ok {
		// we will remove the tag in our taglib anyways.
		// if event.Tag == tag.val, we received an ACK for a previously sent SYN
		// if event.Tag != tag.val, we received a client-initiated SYN, while
		//                          there was another (unanswered) server-initiated SYN
		//                          inflight. In this case, we'll ignore our SYN and process the client SYN
		sess.removeTag(shadow.res.StringRef())
	}
	// check event-tag
	if tag.Val == event.Tag {
		//received a response to a server-side-sync(sss) cycle
		// we're all done!
		// note this relies on the fact that during sync-incoming, appropriate
		// res-taint events have been sent to nofity and we don't have to care
		// about change-propagation anymore at this point and can leave
		return
	}
	// ACK'ing a client's sync-SYN

	// calculate changes and add them to pending and incease our SV
	shadow.UpdatePending(event.store)
	event.Changes = shadow.pending
	if !sess.push_client(event) {
		// edge-case happened: client sent request and disconnected before we
		// could response. set tainted state for resource.
		log.Printf("session[%s]: client went offline during sync, resource (%s)", sess.sid, event.Res.StringRef())
		//TODO the response is now los in the nirvana and the tag never ACK'd. should we expect the client to resend
		// an SYN?
	}
	return
}

func (sess *Session) handle_taint(event Event) {
	log.Printf("session[%s]: handling taint event for %s, all tainted: %s", sess.sid, event.Res, sess.tainted)
	lastFlush := sess.flushes[event.Res.StringRef()]
	if event.ctx.ts.Before(lastFlush) {
		log.Printf("session[%s]: old taint, changes already flushed. event timestamp: %s, last flush: %s", sess.sid, event.ctx.ts, lastFlush)
		return
	}
	sess.markTainted(event.Res)
	log.Printf("session[%s]:  all tainted: %s", sess.sid, sess.tainted)
}

func (sess *Session) handle_reset(event Event) {
	if sess.hasShadow(event.Res) {
		// already exists in shdows. shadow-swap not supported yes
		// this check can be removed later: after the whole "get me the correct empty value"
		// moved into Store, we can simply call sess.addShadow and expect it to check
		// if a shadow already exists
		return
	}
	err := event.store.Load(&event.Res)
	if err != nil {
		return
	}
	// store reset value
	event.Res.Value = event.Res.Value.Empty()
	// TODO(flo) refactor: store needs to implement EmptyValue() and use resourcebackends Empty() as the
	// official place to define empty resource values
	log.Printf("session[%s]: storing new blank resource in shadows %s", sess.sid, event.Res.StringRef())
	sess.addShadow(event.Res)
}

func (sess *Session) handle_session_create(event Event) {
	event.Session = sess
	sess.push_client(event)
}

func (sess *Session) handle_token_consume(event Event) {
	// just echo back, Conn() handled everything for us
	sess.push_client(event)
}

func (sess *Session) handle_snapshot(event Event) {
	event.Session = sess.Snapshot()
	select {
	case event.client <- event:
	default:
		log.Printf("session[%s]: snapshot requested but client gone before response", sess.sid)
	}
}

func (sess *Session) handle_gone(event Event) {
	log.Printf("session[%s]: client gone\n", sess.sid)
	if sess.client != nil {
		close(sess.client)
		sess.client = nil
	}
}

func (sess *Session) handle_notimplemented(event Event) {
	return
}

func (sess *Session) flush(store *Store) {
	// iterate over reset-resources and tainted resources and send syncs to client (if any)
	if sess.client == nil {
		log.Printf("session[%s]: flush requested, but client offline\n", sess.sid)
		// TODO(flo) check if any tags timed out (due to missing client) and taint them again
		return
	}
	log.Printf("session[%s]: flush requested\n", sess.sid)
	for _, res := range sess.tainted {
		shadow, ok := sess.getShadow(res)
		if !ok {
			log.Println("shadow not found, cannot sync", res, sess.shadows)
			continue

		}
		tag, ok := sess.getTag(res.StringRef())
		if ok {
			if time.Now().Sub(tag.LastSent) < 10*time.Second {
				// maybe still inflight, let's wait a little more until we retry
				continue
			}
			// stale tag, resend previous tag, keep resource in tainted state, will be flushed later
			// if this time it get's through, the tag will be removed and the changes still sent
			event := Event{Name: "res-sync", Tag: tag.Val, SID: sess.sid, Res: res.Ref(), Changes: shadow.pending}
			if !sess.push_client(event) {
				// client went offline, stop for now
				log.Printf("session[%s]: client went offline during flush. aborting", sess.sid)
				return
			}
			sess.tagSent(res.StringRef())
			continue
		}
		log.Printf("session[%s]: flushin' tainted resource: %s\n", sess.sid, res)
		modified := shadow.UpdatePending(store)
		if modified {
			newTag := sess.createTag(res.StringRef())
			event := Event{Name: "res-sync", Tag: newTag, SID: sess.sid, Res: res.Ref(), Changes: shadow.pending}
			if !sess.push_client(event) {
				// client went offline, stop for now
				log.Printf("session[%s]: client went offline during flush. aborting", sess.sid)
				return
			}
			sess.tagSent(res.StringRef())
		}
		sess.tickoffTainted(res.Ref())
		sess.flushes[res.StringRef()] = time.Now()
	}
	return
}

func (sess *Session) handle_ehlo(event Event) {
	log.Printf("session[%s]: received client-ehlo. saved new client and flushing changes", sess.sid)
	return
}

func (sess *Session) push_client(event Event) bool {
	select {
	case sess.client <- event:
		log.Printf("session[%s]: pushed event to client: %#v", sess.sid, event)
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

func (sess *Session) getTag(ref string) (Tag, bool) {
	for i := range sess.tags {
		if sess.tags[i].Ref == ref {
			return sess.tags[i], true
		}
	}
	return Tag{}, false
}

func (sess *Session) removeTag(ref string) {
	for i := range sess.tags {
		if sess.tags[i].Ref == ref {
			sess.tags = append(sess.tags[:i], sess.tags[i+1:]...)
			return
		}
	}
}

func (sess *Session) createTag(ref string) string {
	sess.removeTag(ref)
	tag := generateTag()
	sess.tags = append(sess.tags, Tag{Ref: ref, Val: tag, LastSent: time.Time{}})
	return tag
}

func (sess *Session) tagSent(ref string) {
	for i := range sess.tags {
		if sess.tags[i].Ref == ref {
			sess.tags[i].LastSent = time.Now()
		}
	}
}

func (sess *Session) isTainted(res Resource) bool {
	for i := range sess.tainted {
		if sess.tainted[i].Kind == res.Kind && sess.tainted[i].ID == res.ID {
			return true
		}
	}
	return false
}

func (sess *Session) markTainted(res Resource) {
	ref := res.Ref()
	if sess.isTainted(ref) {
		// we are safe here, because the design will enforce that session-changes
		// are never executed concurrently for the same session.
		// each session can modify its own data without having to care
		// about thread safety in regard to that data.
		// There is always at most 1 writer for each session, see SessionHub implementation.
		return
	}
	sess.tainted = append(sess.tainted, ref)
}

func (sess *Session) tickoffTainted(res Resource) {
	for i := range sess.tainted {
		if sess.tainted[i].Kind == res.Kind && sess.tainted[i].ID == res.ID {
			sess.tainted = append(sess.tainted[:i], sess.tainted[i+1:]...)
			return
		}
	}
}

func (sess *Session) addShadow(res Resource) {
	if sess.hasShadow(res) {
		return
	}
	sess.shadows = append(sess.shadows, NewShadow(res))
}

func (sess *Session) getShadow(res Resource) (*Shadow, bool) {
	for i := range sess.shadows {
		if sess.shadows[i].res.ID == res.ID && sess.shadows[i].res.Kind == res.Kind {
			return sess.shadows[i], true
		}
	}
	return nil, false
}

func (sess *Session) hasShadow(res Resource) bool {
	for i := range sess.shadows {
		if sess.shadows[i].res.ID == res.ID && sess.shadows[i].res.Kind == res.Kind {
			return true
		}
	}
	return false
}

func (sess *Session) diff_resources(check []Resource) []Resource {
	news := make([]Resource, 0, len(check))
	for _, res := range check {
		if !sess.hasShadow(res.Ref()) {
			news = append(news, res)
		}
	}
	return news
}

func (sess *Session) Grant(ctx context, action string, res Resource) bool {
	return true
}

func (s *Session) Value() (driver.Value, error) {
	return s.MarshalJSON()
}

func (s *Session) Scan(value interface{}) error {
	return s.UnmarshalJSON(value.([]byte))
}

func (s *Session) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"sid":     s.sid,
		"uid":     s.uid,
		"tags":    s.tags,
		"tainted": s.tainted,
		"shadows": s.shadows,
		"flushes": s.flushes,
	})
}

func (session *Session) UnmarshalJSON(from []byte) error {
	vals := struct {
		SID     string               `json:"sid"`
		UID     string               `json:"uid"`
		Shadows []*Shadow            `json:"shadows"`
		Tainted []Resource           `json:"tainted"`
		Tags    []Tag                `json:"tags"`
		Flushes map[string]time.Time `json:"flushes"`
	}{}
	json.Unmarshal(from, &vals)
	*session = Session{sid: vals.SID,
		uid:     vals.UID,
		tags:    vals.Tags,
		tainted: vals.Tainted,
		shadows: vals.Shadows,
		flushes: vals.Flushes,
	}
	return nil
}

func (err SessionIDInvalidErr) Error() string {
	return fmt.Sprintf("invalid session-id: %s", err.sid)
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

func generateTag() string {
	return randomString(5)
}

func randomString(l int) string {
	const src = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	var bytes = make([]byte, l)
	rand.Read(bytes)
	for i, b := range bytes {
		bytes[i] = src[b%byte(len(src))]
	}
	return string(bytes)
}
