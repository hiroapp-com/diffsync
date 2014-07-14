package diffsync

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"log"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/suite"
)

type SessionTests struct {
	suite.Suite
	srv  *Server
	comm chan CommRequest
}

func (suite *SessionTests) SetupTest() {
	db, err := sql.Open("sqlite3", fmt.Sprintf("./hiro-test.db"))
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
	log.Println("TEST: PING")
	connA := suite.srv.NewConn()
	token, err := suite.srv.anonToken()
	suite.Nil(err, "could not get anon token")
	connA.ClientEvent(Event{Name: "session-create", Token: token})
	resp := suite.awaitResponse(connA.ToClient(), "session-create response did not arrive")
	sessA := resp.Session
	// create new note
	noteRes := suite.addNote(connA, sessA)
	if !suite.NotEqual(Resource{}, noteRes) {
		suite.T().Fatal("could not add resource")
	}
	note := noteRes.Value.(Note)
	suite.NotEmpty(note.SharingToken, "sharingtoken missing in newly created note")

	// now create new anon user to create a sessin using the sharing token
	connB := suite.srv.NewConn()
	connB.ClientEvent(Event{Name: "session-create", Token: note.SharingToken})
	resp = suite.awaitResponse(connB.ToClient(), "session-create response did not arrive")
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
	connA := suite.srv.NewConn()
	token, err := suite.srv.anonToken()
	suite.Nil(err, "could not get anon token")
	connA.ClientEvent(Event{Name: "session-create", Token: token})
	resp := suite.awaitResponse(connA.ToClient(), "session-create response did not arrive")
	sessA := resp.Session
	// create new note
	noteRes := suite.addNote(connA, sessA)
	if !suite.NotEqual(Resource{}, noteRes) {
		suite.T().Fatal("could not add resource")
	}
	//note := noteRes.Value.(Note)
	connA.ClientEvent(Event{Name: "res-sync",
		SID:     sessA.sid,
		Tag:     "test",
		Res:     Resource{Kind: "note", ID: noteRes.ID},
		Changes: []Edit{{SessionClock{0, 1, 0}, NoteDelta{{"invite", "peers/", User{Email: "debug@hiroapp.com"}}}}}})
	resp = suite.awaitResponse(connA.ToClient(), "res-sync response (invite via email) did not arrive")

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
	noteRes := suite.addNote(connA, sessA)
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

func (suite *SessionTests) addNote(conn *Conn, sess *Session) Resource {
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

func randomString(length int) string {
	const src = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	var bytes = make([]byte, length)
	rand.Read(bytes)
	for i, b := range bytes {
		bytes[i] = src[b%byte(len(src))]
	}
	return string(bytes)
}
func TestSessionSuite(t *testing.T) {
	suite.Run(t, new(SessionTests))
}
