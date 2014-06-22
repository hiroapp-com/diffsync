package diffsync

import (
	"errors"
	"fmt"
	"log"
	"time"
    "strings"

	"encoding/json"
)

var (
	_ = log.Print
)

type UnixTime time.Time

type Note struct {
	Title     string    `json:"title"`
	Text      TextValue `json:"text"`
	Peers     PeerList  `json:"peers"`
	CreatedAt UnixTime  `json:"-"`
	CreatedBy User      `json:"-"`
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

type notePatch struct {
	property string
	payload  interface{}
}

func NewNote(text string) Note {
	return Note{Text: TextValue(text), CreatedAt: UnixTime(time.Now()), Peers: []Peer{}}
}

func (t UnixTime) MarshalJSON() ([]byte, error) {
	ts := time.Time(t).UnixNano()
	return json.Marshal(ts)
}

func (t *UnixTime) UnmarshalJSON(from []byte) error {
	var ts int64
	if err := json.Unmarshal(from, &ts); err != nil {
		return err
	}
	*t = UnixTime(time.Unix(0, ts))
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
	case len(path) > 4 && path[:4] != "uid:":
		checkFn = func(p Peer) bool {
			return p.User.UID == path[4:]
		}
	case len(path) > 5 && path[:6] == "email":
		checkFn = func(p Peer) bool {
			return p.User.Email == path[6:]
		}
	case len(path) > 5 && path[:6] == "phone":
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

func (patch notePatch) Patch(val ResourceValue, store *Store) (ResourceValue, error) {
	var err error
	note := val.(Note)
	newnote := note.Clone().(Note)
	switch patch.property {
	case "text":
		txtpatch, ok := patch.payload.(textPatch)
		if !ok {
			return nil, errors.New("invalid textPatch received")
		}
		newtxt, err := txtpatch.Patch(note.Text, store)
		if err != nil {
			return nil, err
		}
		newnote.Text = newtxt.(TextValue)
		// TODO(flo) update last-edit/-seen of current context's peer-entry
	case "title":
		titles := patch.payload.([2]string)
		if note.Title == titles[0] {
			newnote.Title = titles[1]
		}
		// TODO(flo) update last-edit/-seen of current context's peer-entry
	case "peers":
		delta := patch.payload.(NoteDeltaElement)
		switch delta.Op {
		case "invite":
			user := delta.Value.(User)
			// if user.UID: auto-invite user and swap to current user in `peers`
			// if user.Email: search if
			if _, err := getOrCreateUser(&user); err != nil {
				return nil, err
			}
			// TODO(flo) user.inviteToNote(note, context),
			// actually sending out the invite by the means possible and depending if user is signed up or not
			newnote.Peers = append(newnote.Peers, Peer{User: user, Role: "invited"})
			// TODO(flo): add user to requestor's Contacts and send tainted event
		case "set-cursor":
			if idx, ok := newnote.Peers.indexFromPath(delta.Path); ok {
				//TODO(flo) check newnote.Peers[idx].User == context.User
				newnote.Peers[idx].CursorPosition = delta.Value.(int64)
			}
		case "add-peer":
			if len(newnote.Peers) > 0 {
				break
			}
			peer := delta.Value.(Peer)
			now := UnixTime(time.Now())
			peer.LastSeen = &now
			peer.LastEdit = &now
			newnote.Peers = append(newnote.Peers, peer)
		case "rem-peer":
			if idx, ok := newnote.Peers.indexFromPath(delta.Path); ok {
				newnote.Peers = append(newnote.Peers[:idx], newnote.Peers[idx+1:]...)
			}
		}
	}
	return newnote, err
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

func (delta NoteDelta) Apply(to ResourceValue) (ResourceValue, []Patcher, error) {
	original, ok := to.(Note)
	if !ok {
		return nil, nil, errors.New("cannot apply NoteDelta to non Note")
	}
	newres := original.Clone().(Note)
	patches := []Patcher{}
	for _, diff := range delta {
		switch diff.Op {
		case "set-title":
			newres.Title = diff.Value.(string)
			patches = append(patches, notePatch{"title", [2]string{original.Title, diff.Value.(string)}})
		case "delta-text":
			tmpText, textPatches, err := diff.Value.(TextDelta).Apply(original.Text)
			if err != nil {
				// this should not happen. deltas against the shadow must always succeed given synchronized shadows
				return nil, nil, err
			}
			newres.Text = tmpText.(TextValue)
			for i := range textPatches {
				patches = append(patches, notePatch{"text", textPatches[i]})
			}
		case "invite":
			user, ok := diff.Value.(User)
			if !ok || diff.Path != "peers/" {
				break
			}
			newres.Peers = append(newres.Peers, Peer{User: user})
			patches = append(patches, notePatch{"peers", diff})
		case "add-peer":
			peer, ok := diff.Value.(Peer)
			if !ok || diff.Path != "peers/" {
				break
			}
			newres.Peers = append(newres.Peers, peer)
			patches = append(patches, notePatch{"peers", diff})
		case "rem-peer":
			if !strings.HasPrefix(diff.Path, "peers/") {
				break
			}
			idx, ok := newres.Peers.indexFromPath(diff.Path[6:])
			if !ok {
				break
			}
			newres.Peers = append(newres.Peers[:idx], newres.Peers[idx+1:]...)
			patches = append(patches, notePatch{"peers", diff})
		case "set-cursor":
			cursor, ok := diff.Value.(int64)
			if !ok {
				break
			}
			if !strings.HasPrefix(diff.Path, "peers/") {
				break
			}
			if idx, ok := newres.Peers.indexFromPath(diff.Path[6:]); ok {
				newres.Peers[idx].CursorPosition = cursor
				patches = append(patches, notePatch{"peers", diff})
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
	if rhs.LastSeen != nil && lhs.LastSeen != nil && time.Time(*lhs.LastSeen).Before(time.Time(*rhs.LastSeen)) {
		timestamps := Timestamp{Seen: rhs.LastSeen}

		if rhs.LastEdit != nil && time.Time(*lhs.LastEdit).Before(time.Time(*rhs.LastEdit)) {
			timestamps.Edit = rhs.LastEdit
		}
		delta = append(delta, NoteDeltaElement{"set-ts", path, timestamps})
	}
	return delta
}
