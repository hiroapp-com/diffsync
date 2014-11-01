package diffsync

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
)

type SyncResult struct {
	tainted []Resource
}

type Shadow struct {
	res     Resource
	pending []Edit
	Clock
}

type Edit struct {
	Delta  Delta         `json:"delta"`
	Backup ResourceValue `json:"backup"`
	Clock  `json:"clock"`
}

func (e Edit) String() string {
	return fmt.Sprintf("<edit cv/sv: %d/%d, delta: %s>", e.CV, e.SV, e.Delta)
}

func NewShadow(res Resource) *Shadow {
	return &Shadow{
		res:     res,
		pending: []Edit{},
		Clock:   Clock{},
	}
}

func NewSyncResult() *SyncResult {
	return &SyncResult{
		tainted: []Resource{},
	}
}

func (shadow *Shadow) AddEdit(edit Edit) {
	// sanity check if provided edit is alreay in pending
	if len(shadow.pending) > 0 {
		if e := shadow.pending[len(shadow.pending)-1]; e.Clock == edit.Clock && !edit.Delta.HasChanges() && !e.Delta.HasChanges() {
			// dupe
			return
		}
	}
	shadow.pending = append(shadow.pending, edit)
}

func (shadow *Shadow) UpdatePending(forceEmptyDelta bool, store *Store) bool {
	res := shadow.res.Ref()
	log.Printf("shadow[%s]: calculating new delta and upate pending-queue\n", res.StringRef())
	if err := store.Load(&res); err != nil {
		log.Printf("shadow[%s]: error - could not load master-version for update. err: %s", res.StringRef(), err)
	}
	log.Printf("shadow[%s]: current mastertext: `%s`\n", res.StringRef(), res.Value)
	log.Printf("shadow[%s]: current shadowtext: `%s`\n", shadow.res.StringRef(), shadow.res.Value)
	delta := shadow.res.Value.GetDelta(res.Value)
	log.Printf("shadow[%s]: found delta: `%s`\n", res.StringRef(), delta)
	if delta.HasChanges() {
		shadow.AddEdit(Edit{delta, shadow.res.Value, shadow.Clock.Clone()})
		shadow.res = res
		shadow.SV++
		return true
	} else if forceEmptyDelta {
		shadow.AddEdit(Edit{delta, shadow.res.Value, shadow.Clock.Clone()})
		return true
	}
	return false
}

func (s *Shadow) cvCheck(cv int64) (dupe, ok bool) {
	return (s.CV > cv), (s.CV >= cv)
}

func (s *Shadow) svCheck(sv int64) bool {
	if s.SV == sv {
		// everything kosher, move on
		return true
	}
	// Versions diverged, check backups in pending queue for
	log.Println("sessionclock: SV mismatch, restoring backup")
	for i := range s.pending {
		if s.pending[i].SV == sv {
			s.SV = sv
			s.res.Value = s.pending[i].Backup
			s.pending = []Edit{}
			return true
		}
	}
	return false
}

func (shadow *Shadow) SyncIncoming(edit Edit, result *SyncResult, ctx Context) error {
	// Make sure clocks are in sync or recoverable
	log.Printf("shadow[%s]: sync incoming edit: `%v`\n", shadow.res.StringRef(), edit)
	if !shadow.svCheck(edit.SV) {
		return Remark{
			Level: "fatal",
			Slug:  "sv-mismatch",
			Data:  map[string]string{"client": strconv.Itoa(int(edit.SV)), "server": strconv.Itoa(int(shadow.SV))},
		}
	}
	pending := make([]Edit, 0, len(shadow.pending))
	for _, instack := range shadow.pending {
		if edit.SV < instack.SV {
			pending = append(pending, instack)
		}
	}
	shadow.pending = pending
	if dupe, ok := shadow.cvCheck(edit.CV); dupe {
		return nil
	} else if !ok {
		return Remark{
			Level: "fatal",
			Slug:  "cv-mismatch",
			Data:  map[string]string{"client": strconv.Itoa(int(edit.CV)), "server": strconv.Itoa(int(shadow.CV))},
		}
	}
	if !edit.Delta.HasChanges() {
		return nil
	}
	log.Printf("received delta: %s", edit.Delta)
	newres, patches, err := edit.Delta.Apply(shadow.res.Value)
	if err != nil {
		return Remark{
			Level: "error",
			Slug:  "delta-inapplicable",
			Data:  map[string]string{"message": err.Error()},
		}
	}
	shadow.res.Value = newres
	shadow.CV++
	// send patches to store
	for i := range patches {
		log.Printf("found patch: %s", patches[i])
		err := ctx.store.Patch(shadow.res.Ref(), patches[i], result, ctx)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Shadow) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"res":     s.res,
		"pending": s.pending,
		"clock":   s.Clock,
	})
}

func (shadow *Shadow) UnmarshalJSON(from []byte) error {
	// It is rather unfortunate, that we have to implement
	// such a clumsy JSON unmarshaler, taking care of proper
	// deserializing into Interface values.
	// This is merely needed by the sessionstore, because
	// it saves sessiondata (apart from sid, uid) as a
	// json serialized blob.
	// It does not have to be this way. code proper
	// sessinstore or use better serialization (gobs?)
	tmp := struct {
		Res struct {
			Kind     string          `json:"kind"`
			ID       string          `json:"id"`
			RawValue json.RawMessage `json:"val"`
		} `json:"res"`
		Pending []struct {
			Clock     `json:"clock"`
			RawDelta  json.RawMessage `json:"delta"`
			RawBackup json.RawMessage `json:"backup"`
		} `json:"pending"`
		Clock `json:"clock"`
	}{}
	if err := json.Unmarshal(from, &tmp); err != nil {
		return err
	}
	shadow.res = Resource{Kind: tmp.Res.Kind, ID: tmp.Res.ID}
	shadow.Clock = tmp.Clock
	shadow.pending = make([]Edit, len(tmp.Pending))
	switch tmp.Res.Kind {
	case "note":
		note := NewNote("")
		if err := json.Unmarshal(tmp.Res.RawValue, &note); err != nil {
			return err
		}
		shadow.res.Value = note
		for i := range tmp.Pending {
			delta := NoteDelta{}
			backup := NewNote("")
			if err := json.Unmarshal(tmp.Pending[i].RawDelta, &delta); err != nil {
				return err
			}
			if err := json.Unmarshal(tmp.Pending[i].RawBackup, &backup); err != nil {
				return err
			}
			shadow.pending[i] = Edit{Clock: tmp.Pending[i].Clock, Delta: delta, Backup: backup}
		}
	case "folio":
		folio := Folio{}
		if err := json.Unmarshal(tmp.Res.RawValue, &folio); err != nil {
			return err
		}
		shadow.res.Value = folio
		for i := range tmp.Pending {
			delta := FolioDelta{}
			backup := Folio{}
			if err := json.Unmarshal(tmp.Pending[i].RawDelta, &delta); err != nil {
				return err
			}
			if err := json.Unmarshal(tmp.Pending[i].RawBackup, &backup); err != nil {
				return err
			}
			shadow.pending[i] = Edit{Clock: tmp.Pending[i].Clock, Delta: delta, Backup: backup}
		}
	case "profile":
		profile := NewProfile()
		if err := json.Unmarshal(tmp.Res.RawValue, &profile); err != nil {
			return err
		}
		shadow.res.Value = profile
		for i := range tmp.Pending {
			delta := ProfileDelta{}
			backup := NewProfile()
			if err := json.Unmarshal(tmp.Pending[i].RawDelta, &delta); err != nil {
				return err
			}
			if err := json.Unmarshal(tmp.Pending[i].RawBackup, &backup); err != nil {
				return err
			}
			shadow.pending[i] = Edit{Clock: tmp.Pending[i].Clock, Delta: delta, Backup: backup}
		}
	}
	return nil
}

func (sr *SyncResult) Tainted(r Resource) {
	for i := range sr.tainted {
		if r.SameRef(sr.tainted[i]) {
			// exists already
			return
		}
	}
	sr.tainted = append(sr.tainted, r)
}

func (sr *SyncResult) TaintedItems() []Resource {
	return sr.tainted
}
