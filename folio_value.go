package diffsync

import (
	"log"

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

func (delta FolioDelta) Apply(to ResourceValue) (ResourceValue, []Patcher, error) {
	log.Printf("received Folio-Delta: %#v", delta)
	patches := []Patcher{}
	folio := to.Clone().(Folio)
	for _, change := range delta {
		switch change.Op {
		case "add-noteref":
			folio = append(folio, change.Value.(NoteRef))
		case "rem-noteref":
			folio.remove(change.Path)
		//case "swap-noteref": break // only sent by server, never received
		case "set-status":
			if change.Path[:4] != "nid:" {
				continue
			}
			if i, ok := folio.indexFromPath(change.Path); ok {
				folio[i].Status = change.Value.(string)
			}
		default:
			// don't add to patches, we didn't understand the action anyways
			continue
		}
		patches = append(patches, change)
	}
	return folio, patches, nil
}

func (patch FolioChange) Patch(to ResourceValue, store *Store) (ResourceValue, error) {
	log.Printf("received Folio-Patch: %#v", patch)
	folio := to.Clone().(Folio)
	switch patch.Op {
	case "rem-noteref":
		folio.remove(patch.Path)
	case "set-status":
		switch s := patch.Value.(string); s {
		case "active", "archived":
			if i, ok := folio.indexFromPath(patch.Path); ok {
				folio[i].Status = s
			}
		}
	case "add-noteref":
		note := patch.Value.(NoteRef)
		if _, ok := folio.indexFromPath("nid:" + note.NID); ok {
			// already in our folio,
			return folio, nil
		}
		// TODO(flo) check if note with ID already exists. for no just checking against tmp ids
		if len(note.NID) < 4 {
			// save blank note with new NID
			noteStore := store.OpenNew("note")
			// TODO(flo) we have to pass the context all the way down to the store, so we know who is the owner
			res, err := noteStore.CreateEmpty()
			if err != nil {
				return nil, err
			}
			noteStore.NotifyReset(res.ID)
			note.tmpNID = note.NID
			note.NID = res.ID
		}
		// TODO(flo) check permissin?
		folio = append(folio, note)
	}
	return folio, nil
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
		if ok {
			// already existes in old folio, check differences
			if len(old.NID) < 4 {
				// we expect master here to already have the correct NID
				delta = append(delta, FolioChange{"swap-noteref", "nid:" + old.NID, master[i]})
			}
			if old.Status != master[i].Status {
				delta = append(delta, FolioChange{"set-status", "nid:" + master[i].NID, master[i].Status})
			}
			delete(oldExisting, old.NID)
			delete(oldExisting, old.tmpNID)
			continue
		}
		// nothing matched, Looks like a new one!
		cpy := master[i]
		delta = append(delta, FolioChange{"add-noteref", "", cpy})
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

func (folio *Folio) UnmarshalJSON(from []byte) error {
	return json.Unmarshal(from, folio)
}

func generateNID() string {
	return sid_generate()[:10]
}

func grantRead(sid, nid string) bool {
	return true
}
