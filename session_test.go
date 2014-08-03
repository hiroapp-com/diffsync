package diffsync

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"github.com/hiro/hync/comm"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type SessionTests struct {
	dbPath string
	suite.Suite
	srv  *Server
	comm chan comm.Request
}

type Client struct {
	resp    chan Event
	session *Session
}

func NewClient() Client {
	return Client{resp: make(chan Event, 16)}
}

func (client Client) ctx() Context {
	var uid, sid string
	if client.session != nil {
		sid = client.session.sid
		uid = client.session.uid
	}
	return Context{sid: sid, uid: uid, Client: client}
}

func (client Client) awaitResponse() (Event, error) {
	select {
	case event := <-client.resp:
		return event, nil
	case <-time.After(3 * time.Second):
		return Event{}, fmt.Errorf("client response timed out")
	}
	return Event{}, fmt.Errorf("(not so) unreachable?!")
}

func (client Client) Handle(event Event) error {
	if client.session != nil && client.session.sid != event.SID {
		fmt.Println(event.SID, client.session.sid, event)
		panic("client: SID MISMATCH")
	}
	select {
	case client.resp <- event:
	default:
		return fmt.Errorf("cannot send event to client")
	}
	return nil
}

func (suite *SessionTests) SetupTest() {
	suite.dbPath = fmt.Sprintf("./hiro-test-%s.db", randomString(4))
	db, err := sql.Open("sqlite3", suite.dbPath)
	if err != nil {
		suite.T().Fatal("cannot create sqlite db")
	}
	if err = resetDB(db); err != nil {
		suite.T().Fatal("cannot reset db")
	}
	suite.comm = make(chan comm.Request, 1)
	commHandler := func(req comm.Request) error {
		suite.comm <- req
		return nil
	}
	suite.srv, err = NewServer(db, commHandler)
	if err != nil {
		suite.T().Fatal("cannot spawn server", err)
	}
	suite.srv.Store.Mount("note", NewNoteSQLBackend(db))
	suite.srv.Store.Mount("folio", NewFolioSQLBackend(db))
	suite.srv.Store.Mount("profile", NewProfileSQLBackend(db))
	suite.srv.Run()
}

func (suite *SessionTests) TearDownTest() {
	os.Remove(suite.dbPath)
}

func (suite *SessionTests) sessionCreate(token string) Client {
	client := NewClient()
	err := suite.srv.Handle(Event{Name: "session-create", Token: token, ctx: client.ctx()})
	suite.NoError(err, "session-create request failed")
	resp, err := client.awaitResponse()
	suite.NoError(err, "session-create response failed")
	client.session = resp.Session
	return client
}
func (suite *SessionTests) anonSession() Client {
	token, err := suite.srv.anonToken()
	suite.Nil(err, "could not get anon token")
	return suite.sessionCreate(token)
}

func (suite *SessionTests) loginUser(user User) Client {
	token, err := suite.srv.loginToken(user.UID)
	suite.NoError(err, "cannot create login token")
	client := NewClient()
	err = suite.srv.Handle(Event{Name: "session-create", Token: token, ctx: client.ctx()})
	suite.NoError(err, "session-create request failed")
	resp, err := client.awaitResponse()
	suite.NoError(err, "session-create response failed")
	client.session = resp.Session
	return client
}

func (suite *SessionTests) createUserWith2Notes(name string) User {
	store := suite.srv.Store
	ctx := Context{uid: "sys"}
	res, err := store.NewResource("profile", ctx)
	if !suite.NoError(err, "cannot create new profile") {
		suite.T().Fatal("cannot create user")
	}
	user := res.Value.(Profile).User
	result := SyncResult{}
	err = store.Patch(res, Patch{"set-tier", "user/", int64(1), int64(0)}, &result, ctx)
	suite.NoError(err, "cannot lift users tier")
	suite.Equal(1, len(result.tainted), "set-tier did not taint profile")
	// reset result
	result = SyncResult{}
	err = store.Patch(res, Patch{"set-name", "user/", name, ""}, &result, ctx)
	suite.NoError(err, "cannot users name")
	suite.Equal(1, len(result.tainted), "set-name did not taint profile")
	_, err = store.NewResource("note", Context{uid: user.UID})
	suite.NoError(err, "cannot create first note")
	_, err = store.NewResource("note", Context{uid: user.UID})
	suite.NoError(err, "cannot create second note")
	return user
}

func (suite *SessionTests) awaitResponse(ch <-chan Event, timeoutMsg string) Event {
	select {
	case event := <-ch:
		return event
	case <-time.After(5 * time.Second):
		suite.T().Fatal(timeoutMsg)
	}
	return Event{}
}

func (suite *SessionTests) awaitAddPeer(client Client, peerUID string, shared Resource) {
	// see if sessA got hold of the new peer
	peersEvent, err := client.awaitResponse()
	log.Println("TESTETSTEST, (expected peers) event", peersEvent)
	if suite.NoError(err, "inviter didnot receive add-peers event") {
		suite.Equal("res-sync", peersEvent.Name, "got unexpected event-name from connection")
		suite.Equal("note", peersEvent.Res.Kind, "expected a note-sync")
		suite.Equal(shared.ID, peersEvent.Res.ID, "note-id mismatch")
		// expecting 1 or 2 changes is due to th fact, that a possible (email/phone)
		// addNote during withSharingToken left some yet un-acked changes the
		// server sent himself in an ACK. this is normal behaviour, the client
		// will just ignore the first change by its cv/sv
		if suite.Equal(true, 0 < len(peersEvent.Changes), "wrong number of changes") {
			// check the last delta
			deltas := peersEvent.Changes[len(peersEvent.Changes)-1].Delta.(NoteDelta)
			suite.Equal(1, len(deltas), "wrong number of changes")
			suite.Equal("add-peer", deltas[0].Op, "wrong delta.Op")
			suite.Equal(peerUID, deltas[0].Value.(Peer).User.UID, "wrong peer UID")
			suite.Equal("active", deltas[0].Value.(Peer).Role, "wrong peer UID")
		}
	}
}

func (suite *SessionTests) TestVisitorWithAnonToken() {
	// first spawn an anon session that creates the shared token
	token, err := suite.srv.anonToken()
	suite.Nil(err, "could not get anon token")
	client := NewClient()
	err = suite.srv.Handle(Event{Name: "session-create", Token: token, ctx: client.ctx()})
	suite.NoError(err, "session create with anon token failed (request)")
	resp, err := client.awaitResponse()
	suite.NoError(err, "session create with anon token failed (response)")

	suite.NotEqual("", resp.SID, "sid missing in response")
	suite.Equal(token, resp.Token, "response contains wrong token should be `%s`, but was `%s`", token, resp.Token)
	if suite.NotNil(resp.Session, "session missing in response") {
		sess := resp.Session
		suite.Equal(resp.SID, sess.sid, "sid mismatch between event.SID and event.Session.SID")
		suite.NotEmpty(sess.uid, "uid missing in resp.Session")
		suite.Equal(2, len(sess.shadows), "session should contain exactly 2 shadow (profile and folio)")

		shadow := extractShadow(sess, "profile")
		if suite.NotNil(shadow, "returned session did not contain a profile shadow") {
			suite.NotEmpty(shadow.res.ID, "profile-shadow is missing Resource-ID")
			suite.IsType(Profile{}, shadow.res.Value, "Value in profile shadow's resource is not a Profile")
			profile := shadow.res.Value.(Profile)
			suite.Equal(sess.uid, profile.User.UID, "the returned profile shadow has the wrong uid")
		}
		shadow = extractShadow(sess, "folio")
		if suite.NotNil(shadow, "returned session did not contain a folio shadow") {
			suite.NotEmpty(shadow.res.ID, "first (and only) shadow has empty ID")
			suite.IsType(Folio{}, shadow.res.Value, "expected folio-shadow's resource did not contain a valid Folio value")
			folio := shadow.res.Value.(Folio)
			suite.Equal(0, len(folio), "folio with 0 notes expected, but contains `%d`", len(folio))
		}
	}
}

func (suite *SessionTests) TestVisitorWithURLShareToken() {
	// first spawn an anon session that creates the shared token
	clientA := suite.anonSession()
	// create new note
	noteRes := suite.addNote(clientA)
	if !suite.NotEqual(Resource{}, noteRes) {
		suite.T().Fatal("could not add resource")
	}
	note := noteRes.Value.(Note)
	// now create new anon user to create a sessin using the sharing token
	clientB := suite.sessionCreate(note.SharingToken)
	// now check if sessB has all it should have
	sessB := clientB.session
	suite.Equal(3, len(sessB.shadows), "anon user should have 3 shadows (profile, folio, note), but has %d", len(sessB.shadows))
	shadow := extractShadow(sessB, "folio")
	if suite.NotNil(shadow, "returned session did not contain a folio shadow") {
		folio := shadow.res.Value.(Folio)
		if suite.Equal(1, len(folio), "folio has the wrong number of notes. expected 1, got %s", len(folio)) {
			suite.Equal(noteRes.ID, folio[0].NID, "folio contains wrong NID; expected `%s`, got `%s`", noteRes.ID, folio[0].NID)
		}
	}
}

func (suite *SessionTests) TestVisitorWithEmailShareToken() {
	user := suite.createUserWith2Notes("inviter")
	clientA := suite.loginUser(user)
	suite.withShareToken("email", clientA, func(shareToken string, shared Resource) {
		// now use the token to create a new anon-user
		clientB := suite.sessionCreate(shareToken)
		sessB := clientB.session
		// now check if sessB has all it should have
		suite.Equal(3, len(sessB.shadows), "anon user should have 3 shadows (profile, folio, note)")
		shadow := extractShadow(sessB, "folio")
		if suite.NotNil(shadow, "returned session did not contain a folio shadow") {
			folio := shadow.res.Value.(Folio)
			if suite.Equal(1, len(folio), "folio has the wrong number of notes") {
				suite.Equal(shared.ID, folio[0].NID, "folio contains wrong NID")
			}
		}
	})
}

func (suite *SessionTests) TestVisitorWithPhoneShareToken() {
	user := suite.createUserWith2Notes("inviter")
	clientA := suite.loginUser(user)
	suite.withShareToken("phone", clientA, func(shareToken string, shared Resource) {
		// now use the token to create a new anon-user
		clientB := suite.sessionCreate(shareToken)
		sessB := clientB.session
		// now check if sessB has all it should have
		suite.Equal(3, len(sessB.shadows), "anon user should have 3 shadows (profile, folio, note), but has %d", len(sessB.shadows))
		shadow := extractShadow(sessB, "folio")
		if suite.NotNil(shadow, "returned session did not contain a folio shadow") {
			folio := shadow.res.Value.(Folio)
			if suite.Equal(1, len(folio), "folio has the wrong number of notes") {
				suite.Equal(shared.ID, folio[0].NID, "folio contains wrong NID")
			}
		}
	})
}

func (suite *SessionTests) TestVisitorWithLoginToken() {
	user := suite.createUserWith2Notes("test")
	clientA := suite.loginUser(user)

	// check if returned session has user.UID
	shadow := extractShadow(clientA.session, "profile")
	if suite.NotNil(shadow, "profile shaddow missing in session") {
		profile := shadow.res.Value.(Profile)
		suite.Equal(user.UID, profile.User.UID, "new session's profile-user has wrong UID")
		suite.Equal(1, profile.User.Tier, "new session's user is not signed up")
	}
	// check if flio contains 2 notes
	shadow = extractShadow(clientA.session, "folio")
	if suite.NotNil(shadow, "folio shadow missing in session") {
		folio := shadow.res.Value.(Folio)
		suite.Equal(2, len(folio), "folio has the wrong number of notes")
	}
}

func (suite *SessionTests) TestVisitorWithVerifyEmailToken() {
	// first spawn an anon session that creates the shared token
	user := suite.createUserWith2Notes("test")
	clientA := suite.loginUser(user)
	err := suite.srv.Handle(Event{
		Name: "res-sync",
		SID:  clientA.session.sid,
		Tag:  "test",
		Res:  Resource{Kind: "profile", ID: user.UID},
		Changes: []Edit{{
			Clock: SessionClock{0, 0, 0},
			Delta: ProfileDelta{
				UserChange{Op: "set-email", Path: "user/", Value: "test@hiroapp.com"},
			},
		}},
		ctx: clientA.ctx(),
	})
	suite.NoError(err, "cannot set email")
	// check for verification-req
	var req comm.Request
	select {
	case req = <-suite.comm:
	case <-time.After(5 * time.Second):
		suite.T().Fatal("comm-request after set-email did not arrive in time")
	}

	suite.Equal("verify", req.Kind, "comm.Request has wrong kind. expected `verify`, but got `%s`", req, req.Kind)
	if suite.NotEmpty(req.Data["token"], "Token missing in comm.Request atfer invite") {
		// now use the token to create a new anon-user
		clientB := suite.sessionCreate(req.Data["token"])
		sessA := clientB.session
		// now check if sessB has all it should have
		suite.Equal(4, len(sessA.shadows), "verify user should have 4 shadows (profile, folio, 2*note), but has %d", len(sessA.shadows))
		shadow := extractShadow(sessA, "folio")
		if suite.NotNil(shadow, "returned session did not contain a folio shadow") {
			folio := shadow.res.Value.(Folio)
			suite.Equal(2, len(folio), "folio has the wrong number of notes. expected 2, got %s", len(folio))
		}

		// check if returned session matches user.UID
		shadow = extractShadow(sessA, "profile")
		if suite.NotNil(shadow, "returned session did not contain a profile shadow") {
			profile := shadow.res.Value.(Profile)
			suite.Equal(user.UID, profile.User.UID, "new session's profile-user has wrong UID. expected `%s`, got `%s`", user.UID, profile.User.UID)
			suite.Equal(1, profile.User.Tier, "new session's user is not signed up")

			// check if the email is verified
			err := suite.srv.Store.Load(&shadow.res)
			suite.NoError(err, "cannot load profile-data from store")
			suite.Equal("verified", shadow.res.Value.(Profile).User.EmailStatus, "emailstatus is not `verified`, but `%s`", profile.User.EmailStatus)
		}
	}
}

func (suite *SessionTests) TestAnonConsumeURLShare() {
	clientA := suite.anonSession()
	suite.withShareToken("url", clientA, func(shareToken string, shared Resource) {
		// now use the token to create a new anon-user
		clientB := suite.anonSession()
		suite.addNote(clientB)
		suite.srv.Handle(Event{SID: clientB.session.sid, Name: "token-consume", Token: shareToken})
		// we're gonna get 3 responses: the token-consume echo, the folio update and the note-sync
		responses := [3]Event{}
		var err error
		for i := 0; i < 3; i++ {
			responses[i], err = clientB.awaitResponse()
			suite.NoError(err, "token-consume response missing")
		}
		var found int
		for _, resp := range responses {
			switch resp.Name {
			case "token-consume":
				found++
				continue
			case "res-sync":
				if resp.Res.Kind == "note" {
					found++
					suite.Equal(shared.ID, resp.Res.ID, "note-id mismatch. expected `%s`, got `%s`", shared.ID, resp.Res.ID)
					// expecting 3 deltas: set-token & 2*add-peer (sessA and sessB users)
					suite.Equal(3, len(resp.Changes[0].Delta.(NoteDelta)), "wrong number of deltas for note")
				} else if resp.Res.Kind == "folio" {
					found++
					// because they way the protocoll works, the *first* delta the server
					// sent to this session was never acked (b.c. it was sent with an ACK).
					// thus, the server will include the original "set-nid" change along with
					// the actual new-note change
					if suite.Equal(2, len(resp.Changes), "wrong number of changes for folio. expected 2, got %v", resp.Changes) {
						if suite.Equal(1, len(resp.Changes[1].Delta.(FolioDelta)), "wrong number of changes for folio") {
							delta := resp.Changes[1].Delta.(FolioDelta)
							suite.Equal("add-noteref", delta[0].Op, "wrong folio-delta received, wanted a `add-noteref`, got `%s`", delta[0].Op)
							suite.Equal(shared.ID, delta[0].Value.(NoteRef).NID, "wrong nid received in folio-delta")
						}
					}
				}
			default:
				suite.T().Errorf("Received unexpected event: %v", resp)

			}
		}
		suite.Equal(3, found, "not all expected responses received")

	})

}

func (suite *SessionTests) withShareToken(kind string, client Client, fn func(string, Resource)) {
	sharedRes := suite.addNote(client)
	if !suite.NotEqual(Resource{}, sharedRes) {
		suite.T().Fatal("could not add resource")
	}
	var invitee User
	switch kind {
	case "url":
		note := sharedRes.Value.(Note)
		if suite.NotEmpty(note.SharingToken, "sharingtoken missing in newly created note") {
			fn(note.SharingToken, sharedRes)
		}
		return
	case "email":
		invitee = User{Email: "test@hiroapp.com"}
	case "phone":
		invitee = User{Phone: "+100012345"}
	}
	err := suite.srv.Handle(Event{
		Name: "res-sync",
		SID:  client.session.sid,
		Tag:  "test",
		Res:  sharedRes,
		Changes: []Edit{{
			Clock: SessionClock{0, 1, 0},
			Delta: NoteDelta{
				NoteDeltaElement{Op: "invite", Path: "peers/", Value: invitee},
			},
		}},
		ctx: client.ctx(),
	})
	if suite.NoError(err, "could not invite user") {
		// check note update (swap user)
		event, err := client.awaitResponse()
		suite.NoError(err, "inviter did not get note update (add peer) after share")
		suite.Equal("res-sync", event.Name, "got unexpected event-name from connection")
		suite.Equal("note", event.Res.Kind, "expected a note-sync")
		suite.Equal(sharedRes.ID, event.Res.ID, "note-id mismatch")
		if suite.Equal(1, len(event.Changes), "wrong number of changes") {
			// expect swap-user and change-role changes
			if suite.Equal(2, len(event.Changes[0].Delta.(NoteDelta)), "wrong number of deltas") {
				suite.Equal("swap-user", event.Changes[0].Delta.(NoteDelta)[0].Op, "unexpected note-delta")
				suite.Equal("change-role", event.Changes[0].Delta.(NoteDelta)[1].Op, "unexpected note-delta")
			}
			// ACK the sync
			//event.Changes[0].Clock.SV++
			//event.Changes[0].Delta = NoteDelta{}
			//err = suite.srv.Handle(event)
			//suite.NoError(err, "cannot ack add-peer")
		}

		// wait for the folio-change (add contact)
		event, err = client.awaitResponse()
		suite.NoError(err, "invite user response missing after")
		suite.Equal("res-sync", event.Name, "got unexpected event-name from connection")
		suite.Equal("profile", event.Res.Kind, "expected a profile-sync")
		suite.Equal(client.session.uid, event.Res.ID, "profile-id mismatch")
		// one 'add-user' delta expected
		if suite.Equal(1, len(event.Changes), "wrong number of changes") {
			suite.Equal("add-user", event.Changes[0].Delta.(ProfileDelta)[0].Op, "unexpected delta op")
			suite.Equal("contacts/", event.Changes[0].Delta.(ProfileDelta)[0].Path, "unexpected delta path")
			// ACK the sync
			event.Changes[0].Clock.SV++
			event.Changes[0].Delta = ProfileDelta{}
			suite.srv.Handle(event)
		}

		var req comm.Request
		select {
		case req = <-suite.comm:
		case <-time.After(5 * time.Second):
			suite.T().Fatal("comm-request after invite did not arrive in time")
		}
		suite.Equal("invite", req.Kind, "comm.Request has wrong kind")
		if suite.NotEmpty(req.Data["token"], "Token missing in comm.Request atfer invite") {
			fn(req.Data["token"], sharedRes)
		}
	}
}

func (suite *SessionTests) TestAnonConsumeEmailShare() {
	user := suite.createUserWith2Notes("inviter")
	clientA := suite.loginUser(user)
	// create new note
	suite.withShareToken("email", clientA, func(token string, shared Resource) {
		// now use the token to create a new anon-user
		clientB := suite.anonSession()
		suite.addNote(clientB)
		err := suite.srv.Handle(Event{SID: clientB.session.sid, Name: "token-consume", Token: token, ctx: clientB.ctx()})
		suite.NoError(err, "error sending token-consume request")
		// we're gonna get 3 responses: the token-consume echo, the folio update and the note-sync
		responses := [3]Event{}
		for i := 0; i < 3; i++ {
			responses[i], err = clientB.awaitResponse()
			if !suite.NoError(err, "%d response(s) missing after token-consume", 3-i) {
				break
			}
		}
		var found int
		for _, resp := range responses {
			switch resp.Name {
			case "token-consume":
				found++
				continue
			case "res-sync":
				if resp.Res.Kind == "note" {
					found++
					suite.Equal(shared.ID, resp.Res.ID, "note-id mismatch. expected `%s`, got `%s`", shared.ID, resp.Res.ID)
					suite.Equal(4, len(resp.Changes[0].Delta.(NoteDelta)), "wrong number of deltas for note")
				} else if resp.Res.Kind == "folio" {
					found++
					// because they way the protocoll works, the *first* delta the server
					// sent to this session was never acked (b.c. it was sent with an ACK).
					// thus, the server will include the original "set-nid" change along with
					// the actual new-note change
					if suite.Equal(2, len(resp.Changes), "wrong number of changes for folio") {
						if suite.Equal(1, len(resp.Changes[1].Delta.(FolioDelta)), "wrong number of changes for folio") {
							delta := resp.Changes[1].Delta.(FolioDelta)
							suite.Equal("add-noteref", delta[0].Op, "wrong folio-delta received")
							suite.Equal(shared.ID, delta[0].Value.(NoteRef).NID, "wrong nid received in folio-delta")
						}
					}
				}
			default:
				suite.T().Errorf("Received unexpected event: %v", resp)

			}
		}
		suite.Equal(3, found, "not all expected responses received")
		// see if sessA got hold of the new peer
		suite.awaitAddPeer(clientA, clientB.session.uid, shared)
	})
}
func (suite *SessionTests) TestAnonConsumePhoneShare() {
	user := suite.createUserWith2Notes("inviter")
	clientA := suite.loginUser(user)
	suite.withShareToken("phone", clientA, func(token string, shared Resource) {
		// now use the token to create a new anon-user
		clientB := suite.anonSession()
		suite.addNote(clientB)
		err := suite.srv.Handle(Event{SID: clientB.session.sid, Name: "token-consume", Token: token, ctx: clientB.ctx()})
		suite.NoError(err, "cannot consume token")
		// we're gonna get 3 responses: the token-consume echo, the folio update and the note-sync
		responses := [3]Event{}
		for i := 0; i < 3; i++ {
			responses[i], err = clientB.awaitResponse()
			log.Println("RESSPONSES", responses[i])
			if !suite.NoError(err, "%d missing responses", 3-i) {
				break
			}
		}
		var found int
		for _, resp := range responses {
			switch resp.Name {
			case "token-consume":
				found++
				continue
			case "res-sync":
				if resp.Res.Kind == "note" {
					found++
					suite.Equal(shared.ID, resp.Res.ID, "note-id mismatch")
					suite.Equal(4, len(resp.Changes[0].Delta.(NoteDelta)), "wrong number of deltas for note")
				} else if resp.Res.Kind == "folio" {
					found++
					// because they way the protocoll works, the *first* delta the server
					// sent to this session was never acked (b.c. it was sent with an ACK).
					// thus, the server will include the original "set-nid" change along with
					// the actual new-note change
					if suite.Equal(2, len(resp.Changes), "wrong number of changes for folio") {
						if suite.Equal(1, len(resp.Changes[1].Delta.(FolioDelta)), "wrong number of changes for folio") {
							delta := resp.Changes[1].Delta.(FolioDelta)
							suite.Equal("add-noteref", delta[0].Op, "wrong folio-delta received")
							suite.Equal(shared.ID, delta[0].Value.(NoteRef).NID, "wrong nid received in folio-delta")
						}
					}
				}
			default:
				suite.T().Errorf("Received unexpected event: %v", resp)

			}
		}
		suite.Equal(3, found, "not all expected responses received")
		// see if sessA got hold of the new peer
		suite.awaitAddPeer(clientA, clientB.session.uid, shared)
	})
}

func (suite *SessionTests) TestAnonLogin() {
	// first spawn an anon session that creates the shared token
	user := suite.createUserWith2Notes("test")
	token, err := suite.srv.loginToken(user.UID)
	suite.NoError(err, "cannot create login token")
	clientA := suite.anonSession()
	// create new note
	res := suite.addNote(clientA)
	if !suite.NotEqual(Resource{}, res) {
		suite.T().Fatal("could not add resource")
	}

	suite.srv.Handle(Event{SID: clientA.session.sid, Name: "session-create", Token: token, ctx: clientA.ctx()})
	resp, err := clientA.awaitResponse()
	suite.NoError(err, "session-create response did not arrive")
	// overwrite sessA with new session
	sessA := resp.Session
	suite.Equal(user.UID, sessA.uid)
	suite.Equal(5, len(sessA.shadows), "incorrect number of shadows for new session")

	// check if returned session has user.UID
	shadow := extractShadow(sessA, "profile")
	if suite.NotNil(shadow, "returned session did not contain a profile shadow") {
		profile := shadow.res.Value.(Profile)
		suite.Equal(user.UID, profile.User.UID, "new session's profile-user has wrong UID")
		suite.Equal(1, profile.User.Tier, "new session's user is not signed up")
	}
	// check if folio contains 3 notes
	shadow = extractShadow(sessA, "folio")
	if suite.NotNil(shadow, "returned session did not contain a folio shadow") {
		folio := shadow.res.Value.(Folio)
		suite.Equal(3, len(folio), "folio has the wrong number of notes")
	}
}

func (suite *SessionTests) TestAnonWithVerifyEmailToken() {
	user := suite.createUserWith2Notes("test")
	clientA := suite.loginUser(user)
	err := suite.srv.Handle(Event{
		Name: "res-sync",
		SID:  clientA.session.sid,
		Tag:  "test",
		Res:  Resource{Kind: "profile", ID: user.UID},
		Changes: []Edit{{
			Clock: SessionClock{0, 0, 0},
			Delta: ProfileDelta{
				UserChange{Op: "set-email", Path: "user/", Value: "test@hiroapp.com"},
			},
		}},
		ctx: clientA.ctx(),
	})
	suite.NoError(err, "cannot set email")
	// check for verification-req
	var req comm.Request
	select {
	case req = <-suite.comm:
	case <-time.After(5 * time.Second):
		suite.T().Fatal("comm-request after set-email did not arrive in time")
	}

	suite.Equal("verify", req.Kind, "comm.Request has wrong kind. expected `verify`, but got `%s`", req, req.Kind)
	if suite.NotEmpty(req.Data["token"], "Token missing in comm.Request atfer invite") {
		// now use the token to create a new anon-user
		clientA := suite.anonSession()
		// create new note
		res := suite.addNote(clientA)
		if !suite.NotEqual(Resource{}, res) {
			suite.T().Fatal("could not add resource")
		}
		suite.srv.Handle(Event{SID: clientA.session.sid, Name: "session-create", Token: req.Data["token"], ctx: clientA.ctx()})
		resp, err := clientA.awaitResponse()
		suite.NoError(err, "session-create response (of invitee) did not arrive")
		// overwrite old (anon)session
		if resp.Session == nil {
			suite.T().Fatal("empty session received")
		}
		sessA := resp.Session
		suite.Equal(user.UID, sessA.uid)
		for i := range sessA.shadows {
			log.Println("TEST", *sessA.shadows[i])
		}
		suite.Equal(5, len(sessA.shadows), "verify user should have 5 shadows (profile, folio, 3*note)")

		// check if returned session matches user.UID
		shadow := extractShadow(sessA, "profile")
		if suite.NotNil(shadow, "returned session did not contain a profile shadow") {
			profile := shadow.res.Value.(Profile)
			suite.Equal(user.UID, profile.User.UID, "new session's profile-user has wrong UID. expected `%s`, got `%s`", user.UID, profile.User.UID)
			suite.Equal(1, profile.User.Tier, "new session's user is not signed up")

			// check if the email is verified
			err := suite.srv.Store.Load(&shadow.res)
			suite.NoError(err, "cannot load profile-data from store")
			suite.Equal("verified", shadow.res.Value.(Profile).User.EmailStatus, "emailstatus is not `verified`, but `%s`", profile.User.EmailStatus)
		}

		shadow = extractShadow(sessA, "folio")
		if suite.NotNil(shadow, "returned session did not contain a folio shadow") {
			folio := shadow.res.Value.(Folio)
			suite.Equal(3, len(folio), "folio has the wrong number of notes. expected 3, got %s", len(folio))
		}

	}
}

func (suite *SessionTests) TestUserConsumeURLShare() {
	clientA := suite.anonSession()
	// create new note
	suite.withShareToken("url", clientA, func(shareToken string, shared Resource) {
		// now use the token to create a new anon-user
		user := suite.createUserWith2Notes("test")
		token, err := suite.srv.loginToken(user.UID)
		suite.NoError(err, "cannot create login token")
		clientB := suite.sessionCreate(token)
		sessB := clientB.session

		err = suite.srv.Handle(Event{SID: clientB.session.sid, Name: "token-consume", Token: shareToken})
		suite.NoError(err, "cannot consume token")
		// we're gonna get 3 responses: the token-consume echo, the folio update and the note-sync
		responses := [3]Event{}
		for i := 0; i < 3; i++ {
			responses[i], err = clientB.awaitResponse()
			if !suite.NoError(err, "%d responses missing", 3-i) {
				break
			}
		}
		var found int
		for _, resp := range responses {
			switch resp.Name {
			case "token-consume":
				found++
				continue
			case "res-sync":
				if resp.Res.Kind == "note" {
					found++
					suite.Equal(shared.ID, resp.Res.ID, "note-id mismatch")
					// expecting 3 deltas: set-token & 2*add-peer (sessA and sessB users)
					suite.Equal(3, len(resp.Changes[0].Delta.(NoteDelta)), "wrong number of deltas for note")
				} else if resp.Res.Kind == "folio" {
					found++
					if suite.Equal(1, len(resp.Changes), "wrong number of changes for folio") {
						if suite.Equal(1, len(resp.Changes[0].Delta.(FolioDelta)), "wrong number of changes for folio") {
							delta := resp.Changes[0].Delta.(FolioDelta)
							suite.Equal("add-noteref", delta[0].Op, "wrong folio-delta received")
							suite.Equal(shared.ID, delta[0].Value.(NoteRef).NID, "wrong nid received in folio-delta")
						}
					}
				}
			default:
				suite.T().Errorf("Received unexpected event: %v", resp)

			}
		}
		suite.Equal(3, found, "not all expected responses received")
		suite.awaitAddPeer(clientA, sessB.uid, shared)
	})
}

func (suite *SessionTests) TestUserConsumeEmailShare() {
	user := suite.createUserWith2Notes("inviter")
	clientA := suite.loginUser(user)
	suite.withShareToken("email", clientA, func(shareToken string, shared Resource) {
		// now use the token to create a new anon-user
		user := suite.createUserWith2Notes("test")
		token, err := suite.srv.loginToken(user.UID)
		suite.NoError(err, "cannot create login token")
		clientB := suite.sessionCreate(token)
		sessB := clientB.session

		err = suite.srv.Handle(Event{SID: sessB.sid, Name: "token-consume", Token: shareToken, ctx: clientB.ctx()})
		suite.NoError(err, "no respons to token-consume")
		// we're gonna get 3 responses: the token-consume echo, the folio update and the note-sync
		responses := [3]Event{}
		for i := 0; i < 3; i++ {
			responses[i], err = clientB.awaitResponse()
			if !suite.NoError(err, "%d responses missing", 3-i) {
				break
			}
		}
		var found int
		for _, resp := range responses {
			switch resp.Name {
			case "token-consume":
				found++
				continue
			case "res-sync":
				if resp.Res.Kind == "note" {
					found++
					suite.Equal(shared.ID, resp.Res.ID, "note-id mismatch")
					// expecting 3 deltas: set-token & 3*add-peer (sessA, sessB and email-invite user)
					suite.Equal(4, len(resp.Changes[0].Delta.(NoteDelta)), "wrong number of deltas for note")
				} else if resp.Res.Kind == "folio" {
					found++
					if suite.Equal(1, len(resp.Changes), "wrong number of changes for folio") {
						if suite.Equal(1, len(resp.Changes[0].Delta.(FolioDelta)), "wrong number of changes for folio") {
							delta := resp.Changes[0].Delta.(FolioDelta)
							suite.Equal("add-noteref", delta[0].Op, "wrong folio-delta received, wanted a `add-noteref`, got `%s`", delta[0].Op)
							suite.Equal(shared.ID, delta[0].Value.(NoteRef).NID, "wrong nid received in folio-delta")
						}
					}
				}
			default:
				suite.T().Errorf("Received unexpected event: %v", resp)

			}
		}
		suite.Equal(3, found, "not all expected responses received")
		// see if sessA got hold of the new peer
		suite.awaitAddPeer(clientA, sessB.uid, shared)
	})
}

func (suite *SessionTests) TestUserConsumePhoneShare() {
	user := suite.createUserWith2Notes("inviter")
	clientA := suite.loginUser(user)
	suite.withShareToken("phone", clientA, func(shareToken string, shared Resource) {
		// now use the token to create a new anon-user
		user := suite.createUserWith2Notes("test")
		token, err := suite.srv.loginToken(user.UID)
		suite.NoError(err, "cannot create login token")
		clientB := suite.sessionCreate(token)
		sessB := clientB.session

		err = suite.srv.Handle(Event{SID: sessB.sid, Name: "token-consume", Token: shareToken, ctx: clientB.ctx()})
		// we're gonna get 3 responses: the token-consume echo, the folio update and the note-sync
		responses := [3]Event{}
		for i := 0; i < 3; i++ {
			responses[i], err = clientB.awaitResponse()
			if !suite.NoError(err, "%d responses missing", 3-i) {
				break
			}
		}
		var found int
		for _, resp := range responses {
			switch resp.Name {
			case "token-consume":
				found++
				continue
			case "res-sync":
				if resp.Res.Kind == "note" {
					found++
					suite.Equal(shared.ID, resp.Res.ID, "note-id mismatch")
					// expecting 3 deltas: set-token & 3*add-peer (sessA, sessB and email-invite user)
					suite.Equal(4, len(resp.Changes[0].Delta.(NoteDelta)), "wrong number of deltas for note")
				} else if resp.Res.Kind == "folio" {
					found++
					if suite.Equal(1, len(resp.Changes), "wrong number of changes for folio") {
						if suite.Equal(1, len(resp.Changes[0].Delta.(FolioDelta)), "wrong number of changes for folio") {
							delta := resp.Changes[0].Delta.(FolioDelta)
							suite.Equal("add-noteref", delta[0].Op, "wrong folio-delta received, wanted a `add-noteref`, got `%s`", delta[0].Op)
							suite.Equal(shared.ID, delta[0].Value.(NoteRef).NID, "wrong nid received in folio-delta")
						}
					}
				}
			default:
				suite.T().Errorf("Received unexpected event: %v", resp)

			}
		}
		suite.Equal(3, found, "not all expected responses received")
		// see if sessA got hold of the new peer
		suite.awaitAddPeer(clientA, sessB.uid, shared)
	})
}

func (suite *SessionTests) TestInvitedWithVerifyEmailToken() {
	userA := suite.createUserWith2Notes("inviter")
	clientA := suite.loginUser(userA)
	// share to notes wiht test@hiroapp.com
	var shared Resource
	suite.withShareToken("email", clientA, func(shareToken string, res Resource) {
		shared = res
	})

	// create a user who will signup as test and create 1 note on his own
	userB := suite.createUserWith2Notes("test")
	clientB := suite.loginUser(userB)

	err := suite.srv.Handle(Event{
		Name: "res-sync",
		SID:  clientB.session.sid,
		Tag:  "test",
		Res:  Resource{Kind: "profile", ID: userB.UID},
		Changes: []Edit{{
			Clock: SessionClock{0, 0, 0},
			Delta: ProfileDelta{
				UserChange{Op: "set-email", Path: "user/", Value: "test@hiroapp.com"},
			},
		}},
		ctx: clientB.ctx(),
	})
	suite.NoError(err, "cannot set email")
	// await folio sync
	clientB.awaitResponse()
	// check for verification-req
	var req comm.Request
	select {
	case req = <-suite.comm:
	case <-time.After(5 * time.Second):
		suite.T().Fatal("comm-request after set-email did not arrive in time")
	}

	suite.Equal("verify", req.Kind, "comm.Request has wrong kind. expected `verify`")
	if suite.NotEmpty(req.Data["token"], "Token missing in comm.Request atfer email-set") {
		// login "test" user and cosume the verification token
		err := suite.srv.Handle(Event{SID: clientB.session.sid, Name: "session-create", Token: req.Data["token"], ctx: clientB.ctx()})
		suite.NoError(err, "session create response missing")
		resp, err := clientB.awaitResponse()
		suite.NoError(err, "session-create response (of invitee) did not arrive")
		// overwrite old (anon)session
		if resp.Session == nil {
			log.Println("WUWUUWWU %s", resp)
			suite.T().Fatal("empty session received")
		}
		// overwrite with newly created session
		sessB := resp.Session
		suite.Equal(userB.UID, sessB.uid)
		suite.Equal(5, len(sessB.shadows), "verify user should have 5 shadows (profile, folio, 3*note)")

		// check if returned session matches user.UID
		shadow := extractShadow(sessB, "profile")
		if suite.NotNil(shadow, "returned session did not contain a profile shadow") {
			profile := shadow.res.Value.(Profile)
			suite.Equal(userB.UID, profile.User.UID, "new session's profile-user has wrong UID")
			suite.Equal(1, profile.User.Tier, "new session's user is not signed up")

			// check if the email is verified
			err := suite.srv.Store.Load(&shadow.res)
			suite.NoError(err, "cannot load profile-data from store")
			suite.Equal("verified", shadow.res.Value.(Profile).User.EmailStatus, "email is not `verified`")
		}
		shadow = extractShadow(sessB, "folio")
		if suite.NotNil(shadow, "returned session did not contain a folio shadow") {
			folio := shadow.res.Value.(Folio)
			suite.Equal(3, len(folio), "folio has the wrong number of notes")
			found := false
			for i := range folio {
				if shared.ID == folio[i].NID {
					found = true
					break
				}
			}
			suite.Equal(true, found, "previously shared note missing in new folio")
		}
	}
}

func (suite *SessionTests) addNote(client Client) Resource {
	sess := client.session
	err := suite.srv.Handle(Event{Name: "res-sync",
		SID:     sess.sid,
		Tag:     "test",
		Res:     Resource{Kind: "folio", ID: sess.uid},
		Changes: []Edit{{SessionClock{0, 0, 0}, FolioDelta{{"add-noteref", "", NoteRef{NID: "test", Status: "active"}}}}},
		ctx:     client.ctx(),
	})
	suite.NoError(err, "add-noteref failed (request)")
	resp, err := client.awaitResponse()
	suite.NoError(err, "add-noteref failed (response)")
	// check the folio:add-noteref ACK response from server
	res := Resource{Kind: "note"}
	if suite.Equal("folio", resp.Res.Kind, "folio sync expected as first response") {
		for _, edit := range resp.Changes {
			for _, delta := range edit.Delta.(FolioDelta) {
				if delta.Op == "set-nid" {
					res.ID = delta.Value.(string)
				}
			}
		}
		if !suite.NotEmpty(res.ID, "did not receive a NID") {
			return Resource{}
		}
	}
	note := NewNote("")
	// next response should be the initial note-change from the server
	resp, err = client.awaitResponse()
	suite.NoError(err, "initial note-sync after add-note not received")
	if suite.Equal("note", resp.Res.Kind, "note sync expected as second response") {
		deltas := resp.Changes[0].Delta.(NoteDelta)
		for i := range deltas {
			if deltas[i].Op == "set-token" {
				note.SharingToken = deltas[i].Value.(string)
			}
			// todo: handle add-peers deltas
		}
	}
	suite.NotEmpty(note.SharingToken, "did not receive a sharing token fror nid `%s`", res.ID)
	res.Value = note

	// ACK note-sync
	suite.srv.Handle(Event{
		Name:    "res-sync",
		SID:     sess.sid,
		Tag:     resp.Tag,
		Res:     resp.Res,
		Changes: []Edit{{SessionClock{0, 1, 0}, NoteDelta{}}}})
	return res
}

func resetDB(db *sql.DB) error {
	txn, err := db.Begin()
	if err != nil {
		return err
	}
	txn.Exec(DROP_USERS)
	txn.Exec(DROP_NOTES)
	txn.Exec(DROP_TOKENS)
	txn.Exec(DROP_SESSIONS)
	txn.Exec(DROP_CONTACTS)
	txn.Exec(DROP_NOTEREFS)
	txn.Exec(DROP_STRIPETOKENS)

	txn.Exec(CREATE_USERS)
	txn.Exec(CREATE_NOTES)
	txn.Exec(CREATE_TOKENS)
	txn.Exec(CREATE_SESSIONS)
	txn.Exec(CREATE_CONTACTS)
	txn.Exec(CREATE_NOTEREFS)
	txn.Exec(CREATE_STRIPETOKENS)
	txn.Commit()
	return nil
}

func extractShadow(sess *Session, kind string) *Shadow {
	for i := range sess.shadows {
		if sess.shadows[i].res.Kind == kind {
			shadow := sess.shadows[i]
			return shadow
		}
	}
	return nil
}

func TestSessionSuite(t *testing.T) {
	suite.Run(t, new(SessionTests))
}
func TestSessionSerialize(t *testing.T) {
	profile := Profile{User: User{UID: "uid:test"}, Contacts: []User{User{UID: "uid:contact1"}, User{UID: "uid:contact2"}}}
	folio := Folio{NoteRef{NID: "nid:one", Status: "active"}, NoteRef{NID: "nid:two", Status: "archived"}}

	ts := time.Now()
	sess := NewSession("sid:test", "uid:test")
	sess.shadows = append(sess.shadows,
		NewShadow(Resource{Kind: "profile", ID: "uid:test", Value: profile}),
		NewShadow(Resource{Kind: "folio", ID: "uid:test", Value: folio}),
		NewShadow(Resource{Kind: "note", ID: "nid:one", Value: Note{}}),
		NewShadow(Resource{Kind: "note", ID: "nid:two", Value: Note{}}),
	)
	sess.tainted = []Resource{Resource{Kind: "profile", ID: "uid:test"}, Resource{Kind: "note", ID: "nid:two"}}
	sess.tags = []Tag{Tag{Ref: "note:nid:one", Val: "tag-test", LastSent: ts}}
	sess.flushes = map[string]time.Time{"note:nid:one": ts, "profile:uid:test": ts}

	res, err := json.Marshal(sess)
	if assert.NoError(t, err, "cannot marshal session") {
		sess := NewSession("", "")
		err = json.Unmarshal(res, &sess)
		if assert.NoError(t, err, "cannot de-serialize session") {
			assert.Equal(t, 4, len(sess.shadows), "shadow count mismatch after serialization")
			assert.Equal(t, 2, len(sess.tainted), "tainted count mismatch after serialization")
			assert.Equal(t, 1, len(sess.tags), "tag count mismatch after serialization")
			assert.Equal(t, 2, len(sess.flushes), "flush count mismatch after serialization")
		}
	}
}
