package diffsync

import (
	"fmt"
	"log"
	"time"

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
	SessionsOfUser(string) ([]string, error)
	GetSubscriptions(Resource) (map[string]Resource, error)
}

type Auther interface {
	Grant(Context, string, Resource)
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
	client  EventHandler
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

func (sess *Session) setClient(c EventHandler) {
	if c != nil {
		sess.client = c
	}
}

func (sess *Session) Handle(event Event) {
	log.Printf("session[%s]: handling %s event\n", sess.sid[:6], event.Name)
	if event.SID != sess.sid {
		panic("RECEIVED INVALID SID WTF?!")
		return
	}
	switch event.Name {
	case "session-create":
		sess.setClient(event.ctx.Client)
		sess.handle_session_create(event)
	case "token-consume":
		sess.setClient(event.ctx.Client)
		sess.handle_token_consume(event)
	case "res-add":
		sess.handle_add(event)
	case "res-remove":
		sess.handle_remove(event)
	case "res-sync":
		sess.handle_sync(event)
	case "client-ehlo":
		sess.setClient(event.ctx.Client)
		sess.handle_ehlo(event)
	case "client-gone":
		sess.handle_gone(event)
	case "snapshot":
		sess.handle_snapshot(event)
	default:
		sess.handle_notimplemented(event)
	}
	sess.flush(event.ctx)
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
		log.Printf("session[%s]: malformed data; res missing\n", sess.sid[:6])
		return
	}
	if event.Tag == "" {
		// nofitication that something has changed in the item. proceed with tainted flow
		sess.handle_taint(event)
		return
	}
	if event.Changes == nil {
		log.Printf("session[%s]: malformed data; changes missing\n", sess.sid[:6])
		return
	}
	sess.setClient(event.ctx.Client)
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
		event.ctx.LogError(fmt.Errorf("shadow `%s` not found, cannot sync.", event.Res))
		event.Changes = []Edit{}
		event.Remark = &Remark{Level: "fatal", Slug: "shadow-missing"}
		sess.push_client(event)
		return

	}
	result := NewSyncResult()
	for _, edit := range event.Changes {
		err := shadow.SyncIncoming(edit, result, event.ctx)
		if err != nil {
			event.ctx.LogError(err)
			if r, ok := err.(Remark); ok {
				event.Changes = []Edit{}
				event.Remark = &r
				sess.push_client(event)
			}
			return
		}
	}
	for _, res := range result.tainted {
		sess.markTainted(res)
		event.ctx.Router.Handle(Event{Name: "res-sync", Res: res, ctx: event.ctx})
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
	shadow.UpdatePending(true, event.ctx.store)
	event.Changes = shadow.pending
	if !sess.push_client(event) {
		// edge-case happened: client sent request and disconnected before we
		// could response. set tainted state for resource.
		log.Printf("session[%s]: client went offline during sync, resource (%s)", sess.sid[:6], event.Res.StringRef())
		//TODO the response is now los in the nirvana and the tag never ACK'd. should we expect the client to resend
		// an SYN?
	}
	return
}

func (sess *Session) handle_taint(event Event) {
	log.Printf("session[%s]: handling taint event for %s, all tainted: %s", sess.sid[:6], event.Res, sess.tainted)
	lastFlush := sess.flushes[event.Res.StringRef()]
	if event.ctx.ts.Before(lastFlush) {
		log.Printf("session[%s]: old taint, changes already flushed. event timestamp: %s, last flush: %s", sess.sid[:6], event.ctx.ts, lastFlush)
		return
	}
	sess.markTainted(event.Res)
	log.Printf("session[%s]:  all tainted: %s", sess.sid[:6], sess.tainted)
}

func (sess *Session) handle_add(event Event) {
	sess.addShadow(event.Res, event.ctx)
}

func (sess *Session) handle_remove(event Event) {
	sess.removeShadow(event.Res, event.ctx)
}

func (sess *Session) handle_session_create(event Event) {
	event.Session = sess
	if event.ctx.sid != sess.sid {
		// the response to a session-create which was triggered
		// by anothoer session (e.g. anon(sid)->login(token))
		// should address the "old" sid in the Event.
		// The client will receive the new sid for further actions
		// when he takes over the new Event.Session payload
		event.SID = event.ctx.sid
		// tell old session-handler that he should not use its client anymore
		event.ctx.Router.Handle(Event{SID: event.ctx.sid, Name: "client-gone", ctx: event.ctx})
	}
	sess.push_client(event)
}

func (sess *Session) handle_token_consume(event Event) {
	// just echo back, Conn() handled everything for us
	sess.push_client(event)
}

func (sess *Session) handle_snapshot(event Event) {
	event.Session = sess.Snapshot()
	event.ctx.Client.Handle(event)
}

func (sess *Session) handle_gone(event Event) {
	log.Printf("session[%s]: client gone\n", sess.sid[:6])
	if sess.client != nil {
		sess.client = nil
	}
}

func (sess *Session) handle_notimplemented(event Event) {
	return
}

func (sess *Session) flush(ctx Context) {
	// iterate over reset-resources and tainted resources and send syncs to client (if any)
	if sess.client == nil {
		log.Printf("session[%s]: flush requested, but client offline\n", sess.sid[:6])
		// TODO(flo) check if any tags timed out (due to missing client) and taint them again
		return
	}
	log.Printf("session[%s]: flush requested\n", sess.sid[:6])
	for _, res := range sess.tainted {
		shadow, ok := sess.getShadow(res)
		if !ok {
			ctx.LogError(fmt.Errorf("shadow `%s` not found, cannot sync.", res))
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
				log.Printf("session[%s]: client went offline during flush. aborting", sess.sid[:6])
				return
			}
			sess.tagSent(res.StringRef())
			continue
		}
		modified := shadow.UpdatePending(false, ctx.store)
		if modified {
			newTag := sess.createTag(res.StringRef())
			event := Event{Name: "res-sync", Tag: newTag, SID: sess.sid, Res: res.Ref(), Changes: shadow.pending}
			if !sess.push_client(event) {
				// client went offline, stop for now
				log.Printf("session[%s]: client went offline during flush. aborting", sess.sid[:6])
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
	log.Printf("session[%s]: received client-ehlo. saved new client and flushing changes", sess.sid[:6])
	return
}

func (sess *Session) push_client(event Event) (sent bool) {
	if sess.client == nil {
		return false
	}
	if err := sess.client.Handle(event); err != nil {
		log.Printf("session[%s]: error pushing to client: %s", sess.sid[:6], err)
		sess.client = nil
		return false
	}
	log.Printf("session[%s]: PUSH %s", sess.sid[:6], event)
	return true
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
	// only taint if we have a shadow
	for i := range sess.shadows {
		if res.SameRef(sess.shadows[i].res) {
			sess.tainted = append(sess.tainted, ref)
			return
		}
	}
}

func (sess *Session) tickoffTainted(res Resource) {
	for i := range sess.tainted {
		if sess.tainted[i].Kind == res.Kind && sess.tainted[i].ID == res.ID {
			sess.tainted = append(sess.tainted[:i], sess.tainted[i+1:]...)
			return
		}
	}
}

func (sess *Session) addShadow(res Resource, ctx Context) {
	if sess.hasShadow(res) {
		// already exists in shdows. shadow-swap not supported yes
		// this check can be removed later: after the whole "get me the correct empty value"
		// moved into Store, we can simply call sess.addShadow and expect it to check
		// if a shadow already exists
		return
	}
	err := ctx.store.Load(&res)
	if err != nil {
		return
	}
	// store reset value
	res.Value = res.Value.Empty()
	// TODO(flo) refactor: store needs to implement EmptyValue() and use resourcebackends Empty() as the
	// official place to define empty resource values
	log.Printf("session[%s]: storing new blank resource in shadows %s", sess.sid[:6], res.StringRef())
	sess.shadows = append(sess.shadows, NewShadow(res))
}

func (sess *Session) removeShadow(res Resource, ctx Context) {
	for i := range sess.shadows {
		if res.SameRef(sess.shadows[i].res) {
			sess.shadows[i] = sess.shadows[len(sess.shadows)-1]
			sess.shadows[len(sess.shadows)-1] = nil
			sess.shadows = sess.shadows[:len(sess.shadows)-1]
			return
		}
	}
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

func (sess *Session) Grant(ctx Context, action string, res Resource) bool {
	return true
}

func (s *Session) Value() (driver.Value, error) {
	bs, err := s.MarshalJSON()
	return string(bs), err
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

func generateSID() string {
	return hex.EncodeToString(uuid())
}

func generateTag() string {
	return randomString(5)
}
