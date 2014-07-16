package diffsync

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type SessionTests struct {
	dbPath string
	suite.Suite
	srv  *Server
	comm chan CommRequest
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
	suite.comm = make(chan CommRequest, 16)
	suite.srv, err = NewServer(db, suite.comm)
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

func (suite *SessionTests) anonSession() (*Session, *Conn) {
	conn := suite.srv.NewConn()
	token, err := suite.srv.anonToken()
	suite.Nil(err, "could not get anon token")
	conn.ClientEvent(Event{Name: "session-create", Token: token})
	resp := suite.awaitResponse(conn.ToClient(), "session-create response did not arrive")
	return resp.Session, conn
}

func (suite *SessionTests) createUserWith2Notes(name string) User {
	store := suite.srv.Store
	ctx := context{uid: "sys"}
	res, err := store.NewResource("profile", ctx)
	if !suite.NoError(err, "cannot create new profile") {
		suite.T().Fatal("cannot create user")
	}
	user := res.Value.(Profile).User
	err = store.Patch(res, Patch{"set-tier", "user/", int64(1), int64(0)}, ctx)
	suite.NoError(err, "cannot lift users tier")
	err = store.Patch(res, Patch{"set-name", "user/", name, ""}, ctx)
	suite.NoError(err, "cannot users name")
	_, err = store.NewResource("note", context{uid: user.UID})
	suite.NoError(err, "cannot create first note")
	_, err = store.NewResource("note", context{uid: user.UID})
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

func (suite *SessionTests) TestVisitorWithAnonToken() {
	// first spawn an anon session that creates the shared token
	conn := suite.srv.NewConn()
	token, err := suite.srv.anonToken()
	suite.Nil(err, "could not get anon token")
	conn.ClientEvent(Event{Name: "session-create", Token: token})
	resp := suite.awaitResponse(conn.ToClient(), "session-create response did not arrive")

	suite.NotEqual("", resp.SID, "sid missing in response")
	suite.Equal(token, resp.Token, "response contains wrong token should be `%s`, but was `%s`", token, resp.Token)
	if !suite.NotNil(resp.Session, "session missing in response") {
		// test does not make much more sense from here on, thus abort it
		suite.T().Fatal("session missing in response")

	}
	suite.Equal(resp.SID, resp.Session.sid, "sid mismatch between event.SID and event.Session.SID")
	suite.NotEmpty(resp.Session.uid, "uid missing in resp.Session")
	suite.Equal(2, len(resp.Session.shadows), "session should contain exactly 2 shadow (profile and folio). response-session contained %d", len(resp.Session.shadows))

	shadow := extractShadow(resp.Session, "profile")
	if suite.NotNil(shadow, "returned session did not contain a profile shadow") {
		suite.NotEmpty(shadow.res.ID, "profile-shadow is missing Resource-ID")
		suite.IsType(Profile{}, shadow.res.Value, "Value in profile shadow's resource is not a Profile")
		profile := shadow.res.Value.(Profile)
		suite.Equal(resp.Session.uid, profile.User.UID, "the returned profile shadow has the wrong uid")
	}
	shadow = extractShadow(resp.Session, "folio")
	if suite.NotNil(shadow, "returned session did not contain a folio shadow") {
		suite.NotEmpty(shadow.res.ID, "first (and only) shadow has empty ID")
		suite.IsType(Folio{}, shadow.res.Value, "expected folio-shadow's resource did not contain a valid Folio value")
		folio := shadow.res.Value.(Folio)
		suite.Equal(0, len(folio), "folio with 0 notes expected, but contains `%d`", len(folio))
	}
}

func (suite *SessionTests) TestVisitorWithURLShareToken() {
	// first spawn an anon session that creates the shared token
	sessA, connA := suite.anonSession()
	// create new note
	noteRes := suite.addNote(sessA, connA)
	if !suite.NotEqual(Resource{}, noteRes) {
		suite.T().Fatal("could not add resource")
	}
	note := noteRes.Value.(Note)
	suite.NotEmpty(note.SharingToken, "sharingtoken missing in newly created note")

	// now create new anon user to create a sessin using the sharing token
	connB := suite.srv.NewConn()
	connB.ClientEvent(Event{Name: "session-create", Token: note.SharingToken})
	resp := suite.awaitResponse(connB.ToClient(), "session-create response did not arrive")
	sessB := resp.Session
	// now check if sessB has all it should have
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
	// first spawn an anon session that creates the shared token
	sessA, connA := suite.anonSession()
	// create new note
	noteRes := suite.addNote(sessA, connA)
	if !suite.NotEqual(Resource{}, noteRes) {
		suite.T().Fatal("could not add resource")
	}
	//note := noteRes.Value.(Note)
	connA.ClientEvent(Event{Name: "res-sync",
		SID:     sessA.sid,
		Tag:     "test",
		Res:     Resource{Kind: "note", ID: noteRes.ID},
		Changes: []Edit{{SessionClock{0, 1, 0}, NoteDelta{{"invite", "peers/", User{Email: "debug@hiroapp.com"}}}}}})
	resp := suite.awaitResponse(connA.ToClient(), "res-sync response (invite via email) did not arrive")

	// check for emailtoken
	var req CommRequest
	select {
	case req = <-suite.comm:
	case <-time.After(5 * time.Second):
		suite.T().Fatal("comm-request after invite did not arrive in time")
	}
	suite.Equal("email-invite", req.kind, "CommRequest has wrong kind. expected `email-invite`, but go `%s`", req, req.kind)
	if suite.NotEmpty(req.data["token"], "Token missing in CommRequest atfer invite") {
		// now use the token to create a new anon-user
		connB := suite.srv.NewConn()
		connB.ClientEvent(Event{Name: "session-create", Token: req.data["token"]})
		resp = suite.awaitResponse(connB.ToClient(), "session-create response (of invitee) did not arrive")
		sessB := resp.Session
		// now check if sessB has all it should have
		suite.Equal(3, len(sessB.shadows), "anon user should have 3 shadows (profile, folio, note), but has %d", len(sessB.shadows))
		shadow := extractShadow(sessB, "folio")
		if suite.NotNil(shadow, "returned session did not contain a folio shadow") {
			folio := shadow.res.Value.(Folio)
			if suite.Equal(1, len(folio), "folio has the wrong number of notes. expected 1, got %s", len(folio)) {
				suite.Equal(noteRes.ID, folio[0].NID, "folio contains wrong NID; expected `%s`, got `%s`", noteRes.ID, folio[0].NID)
			}
		}
	}
}

func (suite *SessionTests) TestVisitorWithPhoneShareToken() {
	// first spawn an anon session that creates the shared token
	connA := suite.srv.NewConn()
	token, err := suite.srv.anonToken()
	suite.Nil(err, "could not get anon token")
	connA.ClientEvent(Event{Name: "session-create", Token: token})
	resp := suite.awaitResponse(connA.ToClient(), "session-create response did not arrive")
	sessA := resp.Session
	// create new note
	noteRes := suite.addNote(sessA, connA)
	if !suite.NotEqual(Resource{}, noteRes) {
		suite.T().Fatal("could not add resource")
	}
	connA.ClientEvent(Event{Name: "res-sync",
		SID:     sessA.sid,
		Tag:     "test",
		Res:     Resource{Kind: "note", ID: noteRes.ID},
		Changes: []Edit{{SessionClock{0, 1, 0}, NoteDelta{{"invite", "peers/", User{Phone: "+805111111"}}}}}})
	resp = suite.awaitResponse(connA.ToClient(), "res-sync response (invite via email) did not arrive")

	// check for emailtoken
	var req CommRequest
	select {
	case req = <-suite.comm:
	case <-time.After(5 * time.Second):
		suite.T().Fatal("comm-request after phone invite did not arrive in time")
	}
	suite.Equal("phone-invite", req.kind, "CommRequest has wrong kind. expected `phone-invite`, but got `%s`", req, req.kind)
	if suite.NotEmpty(req.data["token"], "Token missing in CommRequest atfer invite") {
		// now use the token to create a new anon-user
		connB := suite.srv.NewConn()
		connB.ClientEvent(Event{Name: "session-create", Token: req.data["token"]})
		resp = suite.awaitResponse(connB.ToClient(), "session-create response (of invitee) did not arrive")
		sessB := resp.Session
		// now check if sessB has all it should have
		suite.Equal(3, len(sessB.shadows), "anon user should have 3 shadows (profile, folio, note), but has %d", len(sessB.shadows))
		shadow := extractShadow(sessB, "folio")
		if suite.NotNil(shadow, "returned session did not contain a folio shadow") {
			folio := shadow.res.Value.(Folio)
			if suite.Equal(1, len(folio), "folio has the wrong number of notes. expected 1, got %s", len(folio)) {
				suite.Equal(noteRes.ID, folio[0].NID, "folio contains wrong NID; expected `%s`, got `%s`", noteRes.ID, folio[0].NID)
			}
		}
	}
}

func (suite *SessionTests) TestVisitorWithLoginToken() {
	// first spawn an anon session that creates the shared token
	user := suite.createUserWith2Notes("test")
	token, err := suite.srv.loginToken(user.UID)
	suite.NoError(err, "cannot create login token")
	connA := suite.srv.NewConn()
	connA.ClientEvent(Event{Name: "session-create", Token: token})
	resp := suite.awaitResponse(connA.ToClient(), "session-create response did not arrive")
	sessA := resp.Session

	// check if returned session has user.UID
	shadow := extractShadow(sessA, "profile")
	if suite.NotNil(shadow, "returned session did not contain a profile shadow") {
		profile := shadow.res.Value.(Profile)
		suite.Equal(user.UID, profile.User.UID, "new session's profile-user has wrong UID. expected `%s`, got `%s`", user.UID, profile.User.UID)
		suite.Equal(1, profile.User.Tier, "new session's user is not signed up")
	}
	// check if flio contains 2 notes
	shadow = extractShadow(sessA, "folio")
	if suite.NotNil(shadow, "returned session did not contain a folio shadow") {
		folio := shadow.res.Value.(Folio)
		suite.Equal(2, len(folio), "folio has the wrong number of notes. expected 2, got %s", len(folio))
	}
}

func (suite *SessionTests) TestVisitorWithVerifyEmailToken() {
	// first spawn an anon session that creates the shared token
	user := suite.createUserWith2Notes("test")
	suite.srv.Store.Patch(Resource{Kind: "profile", ID: user.UID}, Patch{"set-email", "user/", "test@hiroapp.com", ""}, context{uid: user.UID})
	// check for verification-req
	var req CommRequest
	select {
	case req = <-suite.comm:
	case <-time.After(5 * time.Second):
		suite.T().Fatal("comm-request after set-email did not arrive in time")
	}

	suite.Equal("email-verify", req.kind, "CommRequest has wrong kind. expected `email-verify`, but got `%s`", req, req.kind)
	if suite.NotEmpty(req.data["token"], "Token missing in CommRequest atfer invite") {
		// now use the token to create a new anon-user
		connA := suite.srv.NewConn()
		connA.ClientEvent(Event{Name: "session-create", Token: req.data["token"]})
		resp := suite.awaitResponse(connA.ToClient(), "session-create response (of invitee) did not arrive")
		sessA := resp.Session
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
	sessA, connA := suite.anonSession()
	// create new note
	sharedRes := suite.addNote(sessA, connA)
	if !suite.NotEqual(Resource{}, sharedRes) {
		suite.T().Fatal("could not add resource")
	}
	note := sharedRes.Value.(Note)
	if suite.NotEmpty(note.SharingToken, "sharingtoken missing in newly created note") {
		// now use the token to create a new anon-user
		sessB, connB := suite.anonSession()
		suite.addNote(sessB, connB)
		connB.ClientEvent(Event{SID: sessB.sid, Name: "token-consume", Token: note.SharingToken})
		// we're gonna get 3 responses: the token-consume echo, the folio update and the note-sync
		responses := [3]Event{}
		for i := 0; i < 3; i++ {
			responses[i] = suite.awaitResponse(connB.ToClient(), "no response from connection")
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
					suite.Equal(sharedRes.ID, resp.Res.ID, "note-id mismatch. expected `%s`, got `%s`", sharedRes.ID, resp.Res.ID)
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
							suite.Equal(sharedRes.ID, delta[0].Value.(NoteRef).NID, "wrong nid received in folio-delta")
						}
					}
				}
			default:
				suite.T().Errorf("Received unexpected event: %v", resp)

			}
		}
		suite.Equal(3, found, "not all expected responses received")

		// see if sessA got hold of the new peer
		peersEvent := suite.awaitResponse(connA.ToClient(), "inviter did not get peers update")
		suite.Equal("res-sync", peersEvent.Name, "got unexpected event-name from connection: %v", peersEvent.Name)
		suite.Equal("note", peersEvent.Res.Kind, "expected a note-sync, but Res is `%s`", peersEvent.Res.Kind)
		suite.Equal(sharedRes.ID, peersEvent.Res.ID, "note-id mismatch")
		if suite.Equal(1, len(peersEvent.Changes), "wrong number of changes") {
			// see if we got the peers update from sessB
			deltas := peersEvent.Changes[0].Delta.(NoteDelta)
			suite.Equal(1, len(deltas), "wrong number of changes")
			suite.Equal("add-peer", deltas[0].Op, "wrong delta.Op")
			suite.Equal(sessB.uid, deltas[0].Value.(Peer).User.UID, "wrong peer UID")
			suite.Equal("active", deltas[0].Value.(Peer).Role, "wrong peer UID")
		}
	}

}
func (suite *SessionTests) TestAnonConsumeEmailShare() {
	sessA, connA := suite.anonSession()
	// create new note
	sharedRes := suite.addNote(sessA, connA)
	if !suite.NotEqual(Resource{}, sharedRes) {
		suite.T().Fatal("could not add resource")
	}
	err := suite.srv.Store.Patch(sharedRes, Patch{"invite-user", "", User{Email: "test@hiroapp.com"}, nil}, context{uid: sessA.uid, ts: time.Now()})
	if suite.NoError(err, "could not invite user") {
		event := suite.awaitResponse(connA.ToClient(), "inviter did not get profile update (add contact) after email-share")
		suite.Equal("res-sync", event.Name, "got unexpected event-name from connection")
		suite.Equal("profile", event.Res.Kind, "expected a profile-sync")
		suite.Equal(sessA.uid, event.Res.ID, "profile-id mismatch")
		if suite.Equal(1, len(event.Changes), "wrong number of changes") {
			// ACK the sync
			event.Changes[0].Clock.SV++
			event.Changes[0].Delta = ProfileDelta{}
			connA.ClientEvent(event)
		}
		// no await the note update (add-peer)
		event = suite.awaitResponse(connA.ToClient(), "inviter did not get note update (add peer) after email-share")
		suite.Equal("res-sync", event.Name, "got unexpected event-name from connection")
		suite.Equal("note", event.Res.Kind, "expected a note-sync")
		suite.Equal(sharedRes.ID, event.Res.ID, "note-id mismatch")
		if suite.Equal(1, len(event.Changes), "wrong number of changes") {
			if suite.Equal("add-peer", event.Changes[0].Delta.(NoteDelta)[0].Op, "unexpected note-delta") {
				// ACK the sync
				event.Changes[0].Clock.SV++
				event.Changes[0].Delta = NoteDelta{}
				connA.ClientEvent(event)
			}
		}

		var req CommRequest
		select {
		case req = <-suite.comm:
		case <-time.After(5 * time.Second):
			suite.T().Fatal("comm-request after invite did not arrive in time")
		}
		suite.Equal("email-invite", req.kind, "CommRequest has wrong kind. expected `email-invite`, but go `%s`", req, req.kind)
		if suite.NotEmpty(req.data["token"], "Token missing in CommRequest atfer invite") {
			// now use the token to create a new anon-user
			sessB, connB := suite.anonSession()
			suite.addNote(sessB, connB)
			connB.ClientEvent(Event{SID: sessB.sid, Name: "token-consume", Token: req.data["token"]})
			// we're gonna get 3 responses: the token-consume echo, the folio update and the note-sync
			responses := [3]Event{}
			for i := 0; i < 3; i++ {
				responses[i] = suite.awaitResponse(connB.ToClient(), "no response from connection")
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
						suite.Equal(sharedRes.ID, resp.Res.ID, "note-id mismatch. expected `%s`, got `%s`", sharedRes.ID, resp.Res.ID)
						suite.Equal(4, len(resp.Changes[0].Delta.(NoteDelta)), "wrong number of deltas for note")
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
								suite.Equal(sharedRes.ID, delta[0].Value.(NoteRef).NID, "wrong nid received in folio-delta")
							}
						}
					}
				default:
					suite.T().Errorf("Received unexpected event: %v", resp)

				}
			}
			suite.Equal(3, found, "not all expected responses received")

			// see if sessA got hold of the new peer
			peersEvent := suite.awaitResponse(connA.ToClient(), "inviter did not get peers update")
			suite.Equal("res-sync", peersEvent.Name, "got unexpected event-name from connection: %v", peersEvent.Name)
			suite.Equal("note", peersEvent.Res.Kind, "expected a note-sync, but Res is `%s`", peersEvent.Res.Kind)
			suite.Equal(sharedRes.ID, peersEvent.Res.ID, "note-id mismatch")
			if suite.Equal(1, len(peersEvent.Changes), "wrong number of changes") {
				// see if we got the peers update from sessB
				deltas := peersEvent.Changes[0].Delta.(NoteDelta)
				suite.Equal(1, len(deltas), "wrong number of changes")
				suite.Equal("add-peer", deltas[0].Op, "wrong delta.Op")
				suite.Equal(sessB.uid, deltas[0].Value.(Peer).User.UID, "wrong peer UID")
				suite.Equal("active", deltas[0].Value.(Peer).Role, "wrong peer UID")
			}
		}
	}

}
func (suite *SessionTests) TestAnonConsumePhoneShare() {
	sessA, connA := suite.anonSession()
	// create new note
	sharedRes := suite.addNote(sessA, connA)
	if !suite.NotEqual(Resource{}, sharedRes) {
		suite.T().Fatal("could not add resource")
	}
	err := suite.srv.Store.Patch(sharedRes, Patch{"invite-user", "", User{Phone: "+100012345"}, nil}, context{uid: sessA.uid, ts: time.Now()})
	if suite.NoError(err, "could not invite user") {
		event := suite.awaitResponse(connA.ToClient(), "inviter did not get profile update (add contact) after email-share")
		suite.Equal("res-sync", event.Name, "got unexpected event-name from connection")
		suite.Equal("profile", event.Res.Kind, "expected a profile-sync")
		suite.Equal(sessA.uid, event.Res.ID, "profile-id mismatch")
		if suite.Equal(1, len(event.Changes), "wrong number of changes") {
			// ACK the sync
			event.Changes[0].Clock.SV++
			event.Changes[0].Delta = ProfileDelta{}
			connA.ClientEvent(event)
		}
		// no await the note update (add-peer)
		event = suite.awaitResponse(connA.ToClient(), "inviter did not get note update (add peer) after email-share")
		suite.Equal("res-sync", event.Name, "got unexpected event-name from connection")
		suite.Equal("note", event.Res.Kind, "expected a note-sync")
		suite.Equal(sharedRes.ID, event.Res.ID, "note-id mismatch")
		if suite.Equal(1, len(event.Changes), "wrong number of changes") {
			if suite.Equal("add-peer", event.Changes[0].Delta.(NoteDelta)[0].Op, "unexpected note-delta") {
				// ACK the sync
				event.Changes[0].Clock.SV++
				event.Changes[0].Delta = NoteDelta{}
				connA.ClientEvent(event)
			}
		}

		var req CommRequest
		select {
		case req = <-suite.comm:
		case <-time.After(5 * time.Second):
			suite.T().Fatal("comm-request after invite did not arrive in time")
		}
		suite.Equal("phone-invite", req.kind, "CommRequest has wrong kind. expected `phone-invite`, but go `%s`", req, req.kind)
		if suite.NotEmpty(req.data["token"], "Token missing in CommRequest atfer invite") {
			// now use the token to create a new anon-user
			sessB, connB := suite.anonSession()
			suite.addNote(sessB, connB)
			connB.ClientEvent(Event{SID: sessB.sid, Name: "token-consume", Token: req.data["token"]})
			// we're gonna get 3 responses: the token-consume echo, the folio update and the note-sync
			responses := [3]Event{}
			for i := 0; i < 3; i++ {
				responses[i] = suite.awaitResponse(connB.ToClient(), "no response from connection")
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
						suite.Equal(sharedRes.ID, resp.Res.ID, "note-id mismatch. expected `%s`, got `%s`", sharedRes.ID, resp.Res.ID)
						suite.Equal(4, len(resp.Changes[0].Delta.(NoteDelta)), "wrong number of deltas for note")
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
								suite.Equal(sharedRes.ID, delta[0].Value.(NoteRef).NID, "wrong nid received in folio-delta")
							}
						}
					}
				default:
					suite.T().Errorf("Received unexpected event: %v", resp)

				}
			}
			suite.Equal(3, found, "not all expected responses received")

			// see if sessA got hold of the new peer
			peersEvent := suite.awaitResponse(connA.ToClient(), "inviter did not get peers update")
			suite.Equal("res-sync", peersEvent.Name, "got unexpected event-name from connection: %v", peersEvent.Name)
			suite.Equal("note", peersEvent.Res.Kind, "expected a note-sync, but Res is `%s`", peersEvent.Res.Kind)
			suite.Equal(sharedRes.ID, peersEvent.Res.ID, "note-id mismatch")
			if suite.Equal(1, len(peersEvent.Changes), "wrong number of changes") {
				// see if we got the peers update from sessB
				deltas := peersEvent.Changes[0].Delta.(NoteDelta)
				suite.Equal(1, len(deltas), "wrong number of changes")
				suite.Equal("add-peer", deltas[0].Op, "wrong delta.Op")
				suite.Equal(sessB.uid, deltas[0].Value.(Peer).User.UID, "wrong peer UID")
				suite.Equal("active", deltas[0].Value.(Peer).Role, "wrong peer UID")
			}
		}
	}

}

func (suite *SessionTests) TestAnonLogin() {
	// first spawn an anon session that creates the shared token
	user := suite.createUserWith2Notes("test")
	token, err := suite.srv.loginToken(user.UID)
	suite.NoError(err, "cannot create login token")

	sessA, connA := suite.anonSession()
	// create new note
	res := suite.addNote(sessA, connA)
	if !suite.NotEqual(Resource{}, res) {
		suite.T().Fatal("could not add resource")
	}

	connA.ClientEvent(Event{Name: "session-create", Token: token, SID: sessA.sid})
	resp := suite.awaitResponse(connA.ToClient(), "session-create response did not arrive")
	// overwrite sessA with new session
	sessA = resp.Session
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
		suite.Equal(3, len(folio), "folio has the wrong number of notes. expected 3, got %s", len(folio))
	}
}

func (suite *SessionTests) TestAnonWithVerifyEmailToken() {
	// first spawn an anon session that creates the shared token
	user := suite.createUserWith2Notes("test")
	suite.srv.Store.Patch(Resource{Kind: "profile", ID: user.UID}, Patch{"set-email", "user/", "test@hiroapp.com", ""}, context{uid: user.UID})
	// check for verification-req
	var req CommRequest
	select {
	case req = <-suite.comm:
	case <-time.After(5 * time.Second):
		suite.T().Fatal("comm-request after set-email did not arrive in time")
	}

	suite.Equal("email-verify", req.kind, "CommRequest has wrong kind. expected `email-verify`, but got `%s`", req, req.kind)
	if suite.NotEmpty(req.data["token"], "Token missing in CommRequest atfer invite") {
		// now use the token to create a new anon-user
		sessA, connA := suite.anonSession()
		// create new note
		res := suite.addNote(sessA, connA)
		if !suite.NotEqual(Resource{}, res) {
			suite.T().Fatal("could not add resource")
		}
		connA.ClientEvent(Event{SID: sessA.sid, Name: "session-create", Token: req.data["token"]})
		resp := suite.awaitResponse(connA.ToClient(), "session-create response (of invitee) did not arrive")
		// overwrite old (anon)session
		if resp.Session == nil {
			suite.T().Fatal("empty session received")
		}
		sessA = resp.Session
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

func (suite *SessionTests) addNote(sess *Session, conn *Conn) Resource {
	conn.ClientEvent(Event{Name: "res-sync",
		SID:     sess.sid,
		Tag:     "test",
		Res:     Resource{Kind: "folio", ID: sess.uid},
		Changes: []Edit{{SessionClock{0, 0, 0}, FolioDelta{{"add-noteref", "", NoteRef{NID: "test", Status: "active"}}}}}})
	resp := suite.awaitResponse(conn.ToClient(), "add-noteref response (folio-change) did not arrive")
	// first, the ack incl changes to the folio should arrive
	res := Resource{Kind: "note"}
	if suite.Equal("folio", resp.Res.Kind, "first response expected to be of kind `folio`, was `%s`", resp.Res.Kind) {
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
	resp = suite.awaitResponse(conn.ToClient(), "add-noteref response (note-change) did not arrive")
	if suite.Equal("note", resp.Res.Kind, "second response expected to be of kind `note`, was `%s`", resp.Res.Kind) {
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
	conn.ClientEvent(Event{Name: "res-sync",
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
	sess.addShadow(Resource{Kind: "profile", ID: "uid:test", Value: profile})
	sess.addShadow(Resource{Kind: "folio", ID: "uid:test", Value: folio})
	sess.addShadow(Resource{Kind: "note", ID: "nid:one", Value: Note{}})
	sess.addShadow(Resource{Kind: "note", ID: "nid:two", Value: Note{}})
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
