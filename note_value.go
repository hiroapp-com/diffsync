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
	Title     string    `json:"title"`
	Text      TextValue `json:"text"`
	Peers     []Peer    `json:"peers"`
	CreatedAt UnixTime  `json:"-"`
	CreatedBy User      `json:"-"`
}

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

type NoteDelta struct {
	Text   TextDelta   `json:"text"`
	Title  *string     `json:"title"`
	Peers  []PeerDelta `json:"peers"`
	Create bool        `json:"create,omitempty"`
}

type PeerDelta struct {
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
	delta := NewNoteDelta()
	// calculate TextDelta
	delta.Text = note.Text.GetDelta(master.Text).(TextDelta)
	if note.Title != master.Title {
		delta.Title = stringPtr(master.Title)
	}
	// pupulate lookup objects of old versions
	oldExisting := map[string]Peer{}
	oldDangling := []Peer{}
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
			delta.Peers = append(delta.Peers, diffPeerMeta(old, master.Peers[i])...)
			delete(oldExisting, old.User.UID)
			continue
		}
		//TODO(flo) this... hurts in the eyes. fix this.
		if idx, ok := indexOfPeer(fmt.Sprintf("user/email:%s", master.Peers[i].User.Email), oldDangling); master.Peers[i].User.Email != "" && ok {
			delta.Peers = append(delta.Peers, PeerDelta{"swap-user", fmt.Sprintf("user/email:%s", master.Peers[i].User.Email), master.Peers[i].User})
			oldDangling[idx].User = master.Peers[i].User
			delta.Peers = append(delta.Peers, diffPeerMeta(oldDangling[idx], master.Peers[i])...)
			// remove dangling peer from list
			oldDangling = append(oldDangling[:idx], oldDangling[idx+1:]...)
			continue
		} else if idx, ok = indexOfPeer(fmt.Sprintf("user/phone:%s", master.Peers[i].User.Phone), oldDangling); master.Peers[i].User.Phone != "" && ok {
			delta.Peers = append(delta.Peers, PeerDelta{"swap-user", fmt.Sprintf("user/phone:%s", master.Peers[i].User.Phone), master.Peers[i].User})
			oldDangling[idx].User = master.Peers[i].User
			delta.Peers = append(delta.Peers, diffPeerMeta(oldDangling[idx], master.Peers[i])...)
			// remove dangling peer from list
			oldDangling = append(oldDangling[:idx], oldDangling[idx+1:]...)
			continue
		}
		// nothing matched, Looks like a new one!
		cpy := master.Peers[i]
		delta.Peers = append(delta.Peers, PeerDelta{"add-peer", "", cpy})
	}
	for uid, _ := range oldExisting {
		delta.Peers = append(delta.Peers, PeerDelta{Op: "rem-peer", Path: fmt.Sprintf("user/uid:%s", uid)})
	}
	for i := range oldDangling {
		// everything left in the dangling-array did not have a matching entry in the master-list, thus remove
		if oldDangling[i].User.Email != "" {
			delta.Peers = append(delta.Peers, PeerDelta{Op: "rem-peer", Path: fmt.Sprintf("user/email:%s", oldDangling[i].User.Email)})
		} else if oldDangling[i].User.Phone != "" {
			delta.Peers = append(delta.Peers, PeerDelta{Op: "rem-peer", Path: fmt.Sprintf("user/phone:%s", oldDangling[i].User.Phone)})
		}
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
	case "title":
		titles := patch.payload.([2]string)
		if note.Title == titles[0] {
			newnote.Title = titles[1]
		}
	case "peers":
		delta := patch.payload.(PeerDelta)
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
			if idx, ok := indexOfPeer(delta.Path, newnote.Peers); ok {
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
			if idx, ok := indexOfPeer(delta.Path, newnote.Peers); ok {
				newnote.Peers = append(newnote.Peers[:idx], newnote.Peers[idx+1:]...)
			}
		}
	}
	return newnote, err
}

func (pd *PeerDelta) UnmarshalJSON(from []byte) (err error) {
	tmp := struct {
		Op       string          `json:"op"`
		Path     string          `json:"path"`
		RawValue json.RawMessage `json:"value"`
	}{}
	if err = json.Unmarshal(from, &tmp); err != nil {
		return
	}
	pd.Op = tmp.Op
	pd.Path = tmp.Path
	switch tmp.Op {
	case "swap-user", "invite":
		u := User{}
		if err = json.Unmarshal(tmp.RawValue, &u); err == nil {
			pd.Value = u
		}
	case "add-peer":
		p := Peer{}
		if err = json.Unmarshal(tmp.RawValue, &p); err == nil {
			pd.Value = p
		}
	case "set-ts":
		ts := Timestamp{}
		if err = json.Unmarshal(tmp.RawValue, &ts); err == nil {
			pd.Value = ts
		}
	case "set-cursor":
		var i int64
		if err = json.Unmarshal(tmp.RawValue, &i); err == nil {
			pd.Value = i
		}
	default:
		s := ""
		if err = json.Unmarshal(tmp.RawValue, &s); err == nil {
			pd.Value = s
		}
	}
	return
}

func NewNoteDelta() NoteDelta {
	return NoteDelta{Peers: []PeerDelta{}}
}

func (delta NoteDelta) HasChanges() bool {
	if delta.Text.HasChanges() {
		return true
	}
	if delta.Title != nil {
		return true
	}
	if delta.Peers != nil && len(delta.Peers) > 0 {
		return true
	}
	return false
}

func (delta NoteDelta) Apply(to ResourceValue) (ResourceValue, []Patcher, error) {
	original, ok := to.(Note)
	newres := original.Clone().(Note)
	if !ok {
		return nil, nil, errors.New("cannot apply NoteDelta to non Note")
	}
	var err error
	var tmptxt ResourceValue
	patches := []Patcher{}
	if delta.Text.HasChanges() {
		tmptxt, patches, err = delta.Text.Apply(original.Text)
		if err != nil {
			return nil, nil, err
		}
		newres.Text = tmptxt.(TextValue)
		log.Println("PATCH: ", patches)
		for i := range patches {
			patches[i] = notePatch{"text", patches[i]}
		}
		log.Println("PATCH: ", patches)
	}
	if delta.Title != nil {
		patches = append(patches, notePatch{"title", [2]string{newres.Title, *delta.Title}})
		newres.Title = *delta.Title
	}
	for i := range delta.Peers {
		switch delta.Peers[i].Op {
		case "invite":
			if u, ok := delta.Peers[i].Value.(User); ok {
				newres.Peers = append(newres.Peers, Peer{User: u})
				patches = append(patches, notePatch{"peers", delta.Peers[i]})
			}
		case "set-cursor":
			if cursor, ok := delta.Peers[i].Value.(int64); ok {
				if idx, ok := indexOfPeer(delta.Peers[i].Path, newres.Peers); ok {
					newres.Peers[idx].CursorPosition = cursor
					patches = append(patches, notePatch{"peers", delta.Peers[i]})
				}
			}
		case "rem-peer":
			if idx, ok := indexOfPeer(delta.Peers[i].Path, newres.Peers); ok {
				newres.Peers = append(newres.Peers[:idx], newres.Peers[idx+1:]...)
				patches = append(patches, notePatch{"peers", delta.Peers[i]})
			}
		case "add-peer":
			peer := delta.Peers[i].Value.(Peer)
			newres.Peers = append(newres.Peers, peer)
			patches = append(patches, notePatch{"peers", delta.Peers[i]})
		}
	}
	return newres, patches, nil
}

// these functions will be fleshed out and possibly put somewhere else, as soon as we have the proper DB logic
func stringPtr(s string) *string {
	return &s
}

func indexOfPeer(path string, peers []Peer) (idx int, found bool) {
	var chkFn func(Peer) bool
	switch {
	case strings.HasPrefix(path, "user/uid:"):
		chkFn = func(p Peer) bool {
			return p.User.UID == path[9:]
		}
	case strings.HasPrefix(path, "user/email:"):
		chkFn = func(p Peer) bool {
			return p.User.Email == path[11:]
		}
	case strings.HasPrefix(path, "user/phone:"):
		chkFn = func(p Peer) bool {
			return p.User.Phone == path[11:]
		}
	default:
		return 0, false
	}
	for idx = range peers {
		if chkFn(peers[idx]) {
			return idx, true
		}
	}
	return 0, false
}

func diffPeerMeta(lhs, rhs Peer) []PeerDelta {
	deltas := []PeerDelta{}
	path := fmt.Sprintf("user/uid:%s", lhs.User.UID)
	// check if anything changed between old and master
	if lhs.Role != rhs.Role {
		deltas = append(deltas, PeerDelta{"change-role", path, rhs.Role})
	}
	if lhs.CursorPosition != rhs.CursorPosition {
		deltas = append(deltas, PeerDelta{"set-cursor", path, rhs.CursorPosition})
	}
	if rhs.LastSeen != nil && lhs.LastSeen != nil && time.Time(*lhs.LastSeen).Before(time.Time(*rhs.LastSeen)) {
		timestamps := Timestamp{Seen: rhs.LastSeen}

		if rhs.LastEdit != nil && time.Time(*lhs.LastEdit).Before(time.Time(*rhs.LastEdit)) {
			timestamps.Edit = rhs.LastEdit
		}
		deltas = append(deltas, PeerDelta{"set-ts", path, timestamps})
	}
	return deltas
}
