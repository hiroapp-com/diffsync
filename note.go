package diffsync

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"encoding/json"
)

var (
	_ = log.Print
)

type UnixTime time.Time

type Note struct {
	Title        string    `json:"title"`
	Text         TextValue `json:"text"`
	Peers        PeerList  `json:"peers"`
	SharingToken string    `json:"sharing_token"`
	CreatedAt    UnixTime  `json:"created_at"`
	CreatedBy    User      `json:"created_by"`
}

type PeerList []Peer

type Peer struct {
	User           User      `json:"user"`
	CursorPosition int64     `json:"cursor_pos,omitempty"`
	LastSeen       *UnixTime `json:"last_seen,omitempty"`
	LastEdit       *UnixTime `json:"last_edit,omitempty"`
	Role           string    `json:"role"`
}

type Timestamp struct {
	Seen *UnixTime `json:"seen"`
	Edit *UnixTime `json:"edit,omitempty"`
}

type NoteDelta []NoteDeltaElement

type NoteDeltaElement struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

func NewNote(text string) Note {
	return Note{Text: TextValue(text), CreatedAt: UnixTime(time.Time{}), Peers: []Peer{}}
}

func (t *UnixTime) Scan(src interface{}) error {
	tmp, ok := src.(time.Time)
	if !ok {
		return fmt.Errorf("received non-Time value for unixtime from database")
	}
	*t = UnixTime(tmp)
	return nil
}

func (t UnixTime) MarshalJSON() ([]byte, error) {
	ts := time.Time(t).UnixNano()
	// note: we want to output ms, convert ns to ms
	return json.Marshal(ts / 1e6)
}

func (t *UnixTime) UnmarshalJSON(from []byte) error {
	var ts int64
	if err := json.Unmarshal(from, &ts); err != nil {
		return err
	}
	// note: we received ms, convert back to ns
	*t = UnixTime(time.Unix(0, ts*1e6))
	return nil
}

func (pl *PeerList) remove(path string) {
	i, ok := pl.indexFromPath(path)
	if !ok {
		return
	}
	plist := *pl
	plist[i] = plist[len(plist)-1]
	plist = plist[0 : len(plist)-1]
	*pl = plist
}

func (pl PeerList) indexFromPath(path string) (int, bool) {
	var checkFn func(Peer) bool
	switch {
	case len(path) > 4 && path[:4] == "uid:":
		checkFn = func(p Peer) bool {
			return p.User.UID == path[4:]
		}
	case len(path) > 5 && path[:6] == "email:":
		checkFn = func(p Peer) bool {
			return p.User.Email == path[6:]
		}
	case len(path) > 5 && path[:6] == "phone:":
		checkFn = func(p Peer) bool {
			return p.User.Phone == path[6:]
		}
	default:
		return 0, false
	}
	for i := range pl {
		if checkFn(pl[i]) {
			return i, true
		}
	}
	return 0, false
}

func (note Note) Clone() ResourceValue {
	return note
}

func (note Note) Empty() ResourceValue {
	return NewNote("")
}

func (note Note) String() string {
	return fmt.Sprintf("%#v", note)
}

func (note Note) GetDelta(latest ResourceValue) Delta {
	master := latest.(Note)
	delta := NoteDelta{}
	// calculate TextDelta
	if note.Title != master.Title {
		delta = append(delta, NoteDeltaElement{"set-title", "", master.Title})
	}
	if textDelta := note.Text.GetDelta(master.Text).(TextDelta); textDelta.HasChanges() {
		delta = append(delta, NoteDeltaElement{"delta-text", "", textDelta})
	}
	if note.SharingToken != master.SharingToken {
		delta = append(delta, NoteDeltaElement{"set-token", "", master.SharingToken})
	}

	// pupulate lookup objects of old versions
	oldExisting := map[string]Peer{}
	oldDangling := PeerList{}
	for _, peer := range note.Peers {
		if peer.User.UID == "" {
			oldDangling = append(oldDangling, peer)
			continue
		}
		oldExisting[peer.User.UID] = peer
	}
	// now check out the current master-version
	for i := range master.Peers {
		old, ok := oldExisting[master.Peers[i].User.UID]
		if ok {
			delta = append(delta, diffPeerMeta(old, master.Peers[i])...)
			delete(oldExisting, old.User.UID)
			continue
		}
		//TODO(flo) this... hurts in the eyes. fix this.
		if master.Peers[i].User.Email != "" {
			if idx, ok := oldDangling.indexFromPath("email:" + master.Peers[i].User.Email); ok {
				// found current master-entry in old, dangling entries in the comparee.
				delta = append(delta, NoteDeltaElement{"swap-user", oldDangling[idx].User.pathRef("peers"), master.Peers[i].User})
				oldDangling[idx].User = master.Peers[i].User
				// see if anything in the meta-info changed (e.g. last-seen/-edit timestamps, cursor-positions)
				delta = append(delta, diffPeerMeta(oldDangling[idx], master.Peers[i])...)
				// remove from dangling
				oldDangling = append(oldDangling[:idx], oldDangling[idx+1:]...)
				continue
			}
		}
		if master.Peers[i].User.Phone != "" {
			if idx, ok := oldDangling.indexFromPath("phone:" + master.Peers[i].User.Email); ok {
				// found current master-entry in old, dangling entries in the comparee.
				delta = append(delta, NoteDeltaElement{"swap-user", oldDangling[idx].User.pathRef("peers"), master.Peers[i].User})
				oldDangling[idx].User = master.Peers[i].User
				// see if anything in the meta-info changed (e.g. last-seen/-edit timestamps, cursor-positions)
				delta = append(delta, diffPeerMeta(oldDangling[idx], master.Peers[i])...)
				// remove from dangling
				oldDangling = append(oldDangling[:idx], oldDangling[idx+1:]...)
				continue
			}
		}
		// nothing matched, Looks like a new one!
		cpy := master.Peers[i]
		delta = append(delta, NoteDeltaElement{"add-peer", "peers/", cpy})
	}
	for uid, _ := range oldExisting {
		delta = append(delta, NoteDeltaElement{Op: "rem-peer", Path: oldExisting[uid].User.pathRef("peers")})
	}
	for i := range oldDangling {
		// everything left in the dangling-array did not have a matching entry in the master-list, thus remove
		delta = append(delta, NoteDeltaElement{Op: "rem-peer", Path: oldDangling[i].User.pathRef("peers")})
	}
	return delta
}

func (delta *NoteDeltaElement) UnmarshalJSON(from []byte) (err error) {
	tmp := struct {
		Op       string          `json:"op"`
		Path     string          `json:"path"`
		RawValue json.RawMessage `json:"value"`
	}{}
	if err = json.Unmarshal(from, &tmp); err != nil {
		return
	}
	delta.Op = tmp.Op
	delta.Path = tmp.Path
	switch tmp.Op {
	case "invite":
		u := User{}
		if err = json.Unmarshal(tmp.RawValue, &u); err == nil {
			delta.Value = u
		}
	case "add-peer":
		p := Peer{}
		if err = json.Unmarshal(tmp.RawValue, &p); err == nil {
			delta.Value = p
		}
	case "set-ts":
		ts := Timestamp{}
		if err = json.Unmarshal(tmp.RawValue, &ts); err == nil {
			delta.Value = ts
		}
	case "set-cursor":
		var i int64
		if err = json.Unmarshal(tmp.RawValue, &i); err == nil {
			delta.Value = i
		}
	case "delta-text":
		tv := TextDelta("")
		if err = json.Unmarshal(tmp.RawValue, &tv); err == nil {
			delta.Value = tv
		}
	default:
		s := ""
		if err = json.Unmarshal(tmp.RawValue, &s); err == nil {
			delta.Value = s
		}
	}
	return
}

func (delta NoteDelta) HasChanges() bool {
	return len(delta) > 0
}

func (delta NoteDelta) Apply(to ResourceValue) (ResourceValue, []Patch, error) {
	original, ok := to.(Note)
	if !ok {
		return nil, nil, errors.New("cannot apply NoteDelta to non Note")
	}
	newres := original.Clone().(Note)
	patches := []Patch{}
	for _, diff := range delta {
		switch diff.Op {
		case "set-ts":
			// TODO(flo) s/set-ts/set-seen/
			if !strings.HasPrefix(diff.Path, "peers/uid:") {
				break
			}
			patches = append(patches, Patch{Op: "set-seen", Path: diff.Path[10:]})
		case "set-title":
			newres.Title = diff.Value.(string)
			patches = append(patches, Patch{Op: "title", Value: newres.Title, OldValue: original.Title})
		case "delta-text":
			tmpText, textPatches, err := diff.Value.(TextDelta).Apply(original.Text)
			if err != nil {
				// this should not happen. deltas against the shadow must always succeed given synchronized shadows
				return nil, nil, err
			}
			newres.Text = tmpText.(TextValue)
			patches = append(patches, textPatches...)
		case "invite":
			user, ok := diff.Value.(User)
			if !ok || diff.Path != "peers/" {
				break
			}
			newres.Peers = append(newres.Peers, Peer{User: user})
			patches = append(patches, Patch{Op: "invite-user", Value: user})
		case "add-peer":
			peer, ok := diff.Value.(Peer)
			if !ok || diff.Path != "peers/" {
				break
			}
			newres.Peers = append(newres.Peers, peer)
			// add-peer patches are currently not supported by the backende. this also means, that
			// the client will not have the capabilities to perform an add-peer. leaving it in for
			// changes the client might send, which have already be persisted (e.g. add owner peer
			// for new note)
			// Deliberately not creating a patch here
		case "rem-peer":
			if !strings.HasPrefix(diff.Path, "peers/") {
				break
			}
			idx, ok := newres.Peers.indexFromPath(diff.Path[6:])
			if !ok {
				break
			}
			patches = append(patches, Patch{Op: "rem-peer", Path: newres.Peers[idx].User.UID})
			newres.Peers = append(newres.Peers[:idx], newres.Peers[idx+1:]...)
		case "set-cursor":
			cursor, ok := diff.Value.(int64)
			if !ok {
				break
			}
			if !strings.HasPrefix(diff.Path, "peers/") {
				break
			}
			if idx, ok := newres.Peers.indexFromPath(diff.Path[6:]); ok {
				patches = append(patches, Patch{Op: "set-cursor",
					Path:     newres.Peers[idx].User.UID,
					Value:    cursor,
					OldValue: newres.Peers[idx].CursorPosition})
				newres.Peers[idx].CursorPosition = cursor
			}
		}
	}
	return newres, patches, nil
}

// these functions will be fleshed out and possibly put somewhere else, as soon as we have the proper DB logic
func diffPeerMeta(lhs, rhs Peer) NoteDelta {
	delta := NoteDelta{}
	path := lhs.User.pathRef("peers")
	// check if anything changed between old and master
	if lhs.Role != rhs.Role {
		delta = append(delta, NoteDeltaElement{"change-role", path, rhs.Role})
	}
	if lhs.CursorPosition != rhs.CursorPosition {
		delta = append(delta, NoteDeltaElement{"set-cursor", path, rhs.CursorPosition})
	}
	if rhs.LastSeen == nil && lhs.LastSeen == nil {
		//nothing to do here. LastEdit can also not be different, since
		// it cannotbe that last_seen is nil, but not last_edit
		return delta
	} else if lhs.LastSeen == nil || time.Time(*lhs.LastSeen).Before(time.Time(*rhs.LastSeen)) {
		// rhs is now != nil
		timestamps := Timestamp{Seen: rhs.LastSeen}
		if lhs.LastEdit == nil && rhs.LastEdit == nil {
			delta = append(delta, NoteDeltaElement{"set-ts", path, timestamps})
			return delta
		} else if lhs.LastEdit == nil || time.Time(*lhs.LastEdit).Before(time.Time(*rhs.LastEdit)) {
			timestamps.Edit = rhs.LastEdit
			delta = append(delta, NoteDeltaElement{"set-ts", path, timestamps})
		}
	}
	return delta
}
