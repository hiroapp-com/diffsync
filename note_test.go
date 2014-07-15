package diffsync

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNoteSerialize(t *testing.T) {
	ts := time.Now().Round(time.Second)
	unixTs := UnixTime(ts)
	note := Note{Title: "title-test",
		Text:         "text-test",
		SharingToken: "token-test",
		CreatedAt:    unixTs,
		CreatedBy:    User{UID: "uid-owner"},
		Peers: PeerList{
			Peer{User: User{UID: "uid-owner"}},
			Peer{User: User{UID: "uid-peer"}},
		},
	}
	res, err := json.Marshal(note)
	if assert.NoError(t, err, "cannot (json) serialize note") {
		note := NewNote("")
		err = json.Unmarshal(res, &note)
		if assert.NoError(t, err, "cannot (json) de-serialize note") {
			assert.Equal(t, "title-test", note.Title, "title mismatch after serialization")
			assert.Equal(t, "text-test", note.Text, "text mismatch after serialization")
			assert.Equal(t, "token-test", note.SharingToken, "sharing_token mismatch after serialization")
			assert.Equal(t, unixTs, note.CreatedAt, "created_at mismatch after serialization")
			assert.Equal(t, "uid-owner", note.CreatedBy.UID, "created_by.uid mismatch after serialization")
			assert.Equal(t, 2, len(note.Peers), "wrong number of peers after serialization")
		}
	}
}

func TestPeerSerialization(t *testing.T) {
	ts := UnixTime(time.Now().Round(time.Second))
	peer := Peer{User: User{UID: "test-uid"},
		CursorPosition: 23,
		LastSeen:       &ts,
		LastEdit:       &ts,
		Role:           "owner",
	}
	res, err := json.Marshal(peer)
	if assert.NoError(t, err, "cannot marshal peer") {
		peer := Peer{}
		err = json.Unmarshal(res, &peer)
		if assert.NoError(t, err, "cannot de-serialize peer") {
			assert.Equal(t, "test-uid", peer.User.UID, "uid mismatch after serialization")
			assert.Equal(t, 23, peer.CursorPosition, "cursor_position mismatch after serialization")
			assert.Equal(t, ts, *peer.LastSeen, "last_seen mismatch after serialization")
			assert.Equal(t, ts, *peer.LastEdit, "last_edit mismatch after serialization")
			assert.Equal(t, "owner", peer.Role, "role mismatch after serialization")
		}
	}
}
