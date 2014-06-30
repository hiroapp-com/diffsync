package diffsync

import (
	"log"
	"strings"

	"encoding/json"
)

type NoteRef struct {
	NID    string `json:"nid"`
	Status string `json:"status"`
	tmpNID string `json:"-"`
}

type FolioChange struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

type Folio []NoteRef
type FolioDelta []FolioChange

func (folio Folio) Empty() ResourceValue {
	return Folio{}
}

func (folio Folio) Clone() ResourceValue {
	f := make(Folio, len(folio))
	copy(f, folio)
	return f
}

func (f *Folio) remove(path string) Folio {
	i, ok := f.indexFromPath(path)
	if !ok {
		return *f
	}
	folio := *f
	folio[i] = folio[len(folio)-1]
	folio = folio[0 : len(folio)-1]
	*f = folio
	return folio
}

func (f Folio) indexFromPath(path string) (int, bool) {
	if path[:4] != "nid:" {
		// for now, only nid entries may be searched
		return 0, false
	}
	for i := range f {
		if f[i].NID == path[4:] || f[i].tmpNID == path[4:] {
			return i, true
		}
	}
	return 0, false
}

func (d FolioDelta) HasChanges() bool {
	return len(d) > 0
}

func (delta FolioDelta) Apply(to ResourceValue) (ResourceValue, []Patch, error) {
	log.Printf("received Folio-Delta: %#v", delta)
	patches := make([]Patch, 0, len(delta))
	folio := to.Clone().(Folio)
	for _, change := range delta {
		switch change.Op {
		case "add-noteref":
			folio = append(folio, change.Value.(NoteRef))
			patches = append(patches, Patch{Op: "add-noteref", Value: change.Value})
		case "rem-noteref":
			if !strings.HasPrefix(change.Path, "nid:") {
				continue
			}
			folio.remove(change.Path)
			patches = append(patches, Patch{Op: "rem-noteref", Value: change.Path[4:]})
		//case "set-nid", "swap-noteref": continue // only sent by server, never received
		case "set-status":
			if !strings.HasPrefix(change.Path, "nid:") {
				continue
			}
			if i, ok := folio.indexFromPath(change.Path); ok {
				patch := Patch{Op: "set-status", Path: change.Path[4:], OldValue: folio[i].Status}
				folio[i].Status = change.Value.(string)
				patch.Value = folio[i].Status
				patches = append(patches, patch)
			}
		default:
			// don't add to patches, we didn't understand the action anyways
			continue
		}
	}
	return folio, patches, nil
}

func (change *FolioChange) UnmarshalJSON(from []byte) (err error) {
	tmp := struct {
		Op       string          `json:"op"`
		Path     string          `json:"path"`
		RawValue json.RawMessage `json:"value"`
	}{}
	if err = json.Unmarshal(from, &tmp); err != nil {
		return
	}
	change.Op = tmp.Op
	change.Path = tmp.Path
	switch tmp.Op {
	case "add-noteref", "swap-noteref":
		nr := NoteRef{}
		if err = json.Unmarshal(tmp.RawValue, &nr); err == nil {
			change.Value = nr
		}
	default:
		s := ""
		if err = json.Unmarshal(tmp.RawValue, &s); err == nil {
			change.Value = s
		}
	}
	return
}

func (folio Folio) GetDelta(latest ResourceValue) Delta {
	delta := FolioDelta{}
	master := latest.(Folio)

	oldExisting := map[string]NoteRef{}
	for _, noteref := range folio {
		// fill references
		if noteref.NID != "" {
			oldExisting[noteref.NID] = noteref
		} else if noteref.tmpNID != "" {
			oldExisting[noteref.tmpNID] = noteref
		}
	}
	// now check out the current master-version
	for i := range master {
		old, ok := oldExisting[master[i].NID]
		if !ok {
			old, ok = oldExisting[master[i].tmpNID]
		}
		if !ok {
			// Looks like a new one!
			cpy := master[i]
			delta = append(delta, FolioChange{"add-noteref", "", cpy})
			continue
		}
		// already existes in old folio, check differences
		if old.NID != master[i].NID {
			delta = append(delta, FolioChange{"set-nid", "nid:" + old.NID, master[i].NID})
		}
		if old.Status != master[i].Status {
			delta = append(delta, FolioChange{"set-status", "nid:" + master[i].NID, master[i].Status})
		}
		delete(oldExisting, old.NID)
		delete(oldExisting, old.tmpNID)
	}
	// remove everything that's left in the bag.
	for _, old := range oldExisting {
		if old.NID != "" {
			delta = append(delta, FolioChange{Op: "rem-noteref", Path: "nid:" + old.NID})
		} else if old.tmpNID != "" {
			delta = append(delta, FolioChange{Op: "rem-noteref", Path: "nid:" + old.tmpNID})
		}
	}
	return delta
}

func (folio Folio) String() string {
	s, _ := json.MarshalIndent(folio, "", "  ")
	return string(s)
}

func grantRead(sid, nid string) bool {
	return true
}
