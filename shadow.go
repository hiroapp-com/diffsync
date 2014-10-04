package diffsync

import (
	"encoding/json"
	"fmt"
	"log"
)

type SyncResult struct {
	tainted []Resource
}

type Shadow struct {
	res     Resource
	backup  ResourceValue
	pending []Edit
	SessionClock
}

type Edit struct {
	Clock SessionClock `json:"clock"`
	Delta Delta        `json:"delta"`
}

func (e Edit) String() string {
	return fmt.Sprintf("<edit cv/sv: %d/%d, delta: %s>", e.Clock.CV, e.Clock.SV, e.Delta)
}

func NewShadow(res Resource) *Shadow {
	return &Shadow{
		res:          res,
		backup:       res.Value.Clone(),
		pending:      []Edit{},
		SessionClock: SessionClock{},
	}
}

func NewSyncResult() *SyncResult {
	return &SyncResult{
		tainted: []Resource{},
	}
}

func (shadow *Shadow) Rollback() {
	shadow.res.Value = shadow.backup
	shadow.pending = []Edit{}
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
		shadow.AddEdit(Edit{shadow.SessionClock.Clone(), delta})
		shadow.res = res
		shadow.IncSv()
		return true
	} else if forceEmptyDelta {
		shadow.AddEdit(Edit{shadow.SessionClock.Clone(), delta})
		return true
	}
	return false
}
func (shadow *Shadow) SyncIncoming(edit Edit, result *SyncResult, ctx Context) error {
	// Make sure clocks are in sync or recoverable
	log.Printf("shadow[%s]: sync incoming edit: `%v`\n", shadow.res.StringRef(), edit)
	if err := shadow.SessionClock.SyncSvWith(edit.Clock, shadow); err != nil {
		return err
	}
	pending := make([]Edit, 0, len(shadow.pending))
	for _, instack := range shadow.pending {
		if !edit.Clock.Ack(instack.Clock) {
			pending = append(pending, instack)
		}
	}
	shadow.pending = pending
	if dupe, err := shadow.CheckCV(edit.Clock); dupe {
		return nil
	} else if err != nil {
		log.Printf("err sync cv")
		return err
	}
	if !edit.Delta.HasChanges() {
		return nil
	}
	log.Printf("received delta: %s", edit.Delta)
	newres, patches, err := edit.Delta.Apply(shadow.res.Value)
	if err != nil {
		return err
	}
	shadow.res.Value = newres
	shadow.backup = newres
	shadow.Checkpoint()
	shadow.IncCv()
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
		"backup":  s.backup,
		"pending": s.pending,
		"clock":   s.SessionClock,
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
		Backup  json.RawMessage `json:"backup"`
		Pending []struct {
			Clock    SessionClock    `json:"clock"`
			RawDelta json.RawMessage `json:"delta"`
		} `json:"pending"`
		SessionClock `json:"clock"`
	}{}
	if err := json.Unmarshal(from, &tmp); err != nil {
		return err
	}
	shadow.res = Resource{Kind: tmp.Res.Kind, ID: tmp.Res.ID}
	shadow.SessionClock = tmp.SessionClock
	shadow.pending = make([]Edit, len(tmp.Pending))
	switch tmp.Res.Kind {
	case "note":
		note := NewNote("")
		backup := NewNote("")
		if err := json.Unmarshal(tmp.Res.RawValue, &note); err != nil {
			return err
		}
		if err := json.Unmarshal(tmp.Backup, &backup); err != nil {
			return err
		}
		shadow.res.Value = note
		shadow.backup = backup
		for i := range tmp.Pending {
			delta := NoteDelta{}
			if err := json.Unmarshal(tmp.Pending[i].RawDelta, &delta); err != nil {
				return err
			}
			shadow.pending[i] = Edit{Clock: tmp.Pending[i].Clock, Delta: delta}
		}
	case "folio":
		folio := Folio{}
		backup := Folio{}
		if err := json.Unmarshal(tmp.Res.RawValue, &folio); err != nil {
			return err
		}
		if err := json.Unmarshal(tmp.Backup, &backup); err != nil {
			return err
		}
		shadow.res.Value = folio
		shadow.backup = backup
		for i := range tmp.Pending {
			delta := FolioDelta{}
			if err := json.Unmarshal(tmp.Pending[i].RawDelta, &delta); err != nil {
				return err
			}
			shadow.pending[i] = Edit{Clock: tmp.Pending[i].Clock, Delta: delta}
		}
	case "profile":
		profile := NewProfile()
		backup := NewProfile()
		if err := json.Unmarshal(tmp.Res.RawValue, &profile); err != nil {
			return err
		}
		if err := json.Unmarshal(tmp.Backup, &backup); err != nil {
			return err
		}
		shadow.res.Value = profile
		shadow.backup = backup
		for i := range tmp.Pending {
			delta := ProfileDelta{}
			if err := json.Unmarshal(tmp.Pending[i].RawDelta, &delta); err != nil {
				return err
			}
			shadow.pending[i] = Edit{Clock: tmp.Pending[i].Clock, Delta: delta}
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
