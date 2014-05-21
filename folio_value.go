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

func (ref NoteRef) diff(latest NoteRef) (changes [][4]string) {
	if latest.Status != ref.Status {
		changes = append(changes, [4]string{ref.NID, "status", ref.Status, latest.Status})
	}
	if latest.NID != ref.NID {
		changes = append(changes, [4]string{ref.NID, "nid", ref.NID, latest.NID})
	}
	return
}

func (ref *NoteRef) setStatus(from, to string) {
	if ref.Status != from {
		return
	}
	switch to {
	case "active":
		fallthrough
	case "archive":
		ref.Status = to
	}
}

type Folio []NoteRef

func (folio Folio) CloneValue() ResourceValue {
	f := make(Folio, len(folio))
	copy(f, folio)
	return f
}

func (f *Folio) remove(ref NoteRef) Folio {
	folio := *f
	for i := range folio {
		if folio[i].NID == ref.NID {
			folio[i] = folio[len(folio)-1]
			folio = folio[0 : len(folio)-1]
			*f = folio
			break
		}
	}
	return folio
}

type FolioDelta struct {
	Additions     []NoteRef   `json:"add"`
	Removals      []NoteRef   `json:"rem"`
	Modifications [][4]string `json:"mod"` // [id, field, old, new]
}

func (d FolioDelta) HasChanges() bool {
	return (len(d.Additions) + len(d.Removals) + len(d.Modifications)) > 0
}

func NewFolioDelta() FolioDelta {
	return FolioDelta{}
}

func (delta FolioDelta) Apply(to ResourceValue) (ResourceValue, []Patcher, error) {
	log.Printf("received Folio-Delta: %#v", delta)
	folio := to.CloneValue().(Folio)
	for i := range delta.Removals {
		folio.remove(delta.Removals[i])
	}
	folio = append(folio, delta.Additions...)
	mods := map[string][][]string{}
	for _, mod := range delta.Modifications {
		mods[mod[0]] = append(mods[mod[0]], mod[1:])
	}
	// iterate over all folio's notes and check if delta contains modifications for it
	for i := range folio {
		for _, change := range mods[folio[i].NID] {
			switch change[0] {
			case "status":
				folio[i].setStatus(change[1], change[2])
			}
		}
	}
	return folio, []Patcher{to.GetDelta(folio).(FolioDelta)}, nil
}

func (patch FolioDelta) Patch(to ResourceValue, notify chan<- Event) (ResourceValue, error) {
	log.Printf("received Folio-Patch: %#v", patch)
	folio := to.CloneValue().(Folio)
	for i := range patch.Removals {
		// remove is idempodent anyways, no need to check whether it existed or not
		folio.remove(patch.Removals[i])
	}

	for _, ref := range patch.Additions {
		switch ref.Status {
		case "active":
			fallthrough
		case "archive":
			break
		default:
			ref.Status = "active"
		}
		//TODO(flo) check store if NID exsist, if not create new one. using custom prefix for now
		if strings.HasPrefix(ref.NID, "new:") {
			//TODO(flo): figure out proper client-file creation and payload sending
			// proper way would be to first let the client send the create event to the folio
			// and upon receiving a proper NID, it can send all contents and infos as a normal sync event
			ref.tmpNID = ref.NID
			ref.NID = generateNID()
			folio = append(folio, ref)
			continue
		}
		//TODO(flo): check permissions of current session to NID
		if grantRead("TODO:sid", ref.NID) {
			folio = append(folio, ref)
			continue
		}
	}
	mods := map[string][][]string{}
	for _, mod := range patch.Modifications {
		mods[mod[0]] = append(mods[mod[0]], mod[1:])
	}
	// iterate over all folio's notes and check if delta contains modifications for it
	for i := range folio {
		for _, change := range mods[folio[i].NID] {
			switch change[0] {
			// also setStatus is idempodent and will only perform the change if old is same as current
			case "status":
				folio[i].setStatus(change[1], change[2])
			}
		}
	}
	return folio, nil
}

func (folio Folio) GetDelta(latest ResourceValue) Delta {
	delta := NewFolioDelta()
	tmp := map[string]NoteRef{}
	for _, ref := range folio {
		// fill lookup map with LHS values
		tmp[ref.NID] = ref
	}
	for _, ref := range latest.(Folio) {
		if ref.tmpNID != "" {
			if r, ok := tmp[ref.tmpNID]; ok {
				delta.Modifications = append(delta.Modifications, r.diff(ref)...)
				delete(tmp, ref.tmpNID)
				continue
			}
		}
		// we expect all refs in latest to have a non-empty NID
		if r, ok := tmp[ref.NID]; ok {
			delta.Modifications = append(delta.Modifications, r.diff(ref)...)
			delete(tmp, ref.NID)
		} else {
			delta.Additions = append(delta.Additions, ref)
		}
	}
	for k := range tmp {
		// everything left in tmp was not filtered out before and thus was removed in master
		delta.Removals = append(delta.Removals, tmp[k])
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
