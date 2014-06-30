package diffsync

import (
	"encoding/json"
	"log"
)

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

func NewShadow(res Resource) *Shadow {
	return &Shadow{
		res:          res,
		backup:       res.Value.Clone(),
		pending:      []Edit{},
		SessionClock: SessionClock{},
	}
}

func (shadow *Shadow) Rollback() {
	shadow.res.Value = shadow.backup
	shadow.pending = []Edit{}
}

func (shadow *Shadow) UpdatePending(store *Store) error {
	res := shadow.res.Ref()
	log.Printf("shadow[%s:%p]: calculating new delta and upate pending-queue\n", res.StringRef(), &res)
	_ = store.Load(&res)
	log.Printf("shadow[%s:%p]: current mastertext: `%s`\n", res.StringRef(), &res, res.Value)
	log.Printf("shadow[%s:%p]: current shadowtext: `%s`\n", shadow.res.StringRef(), &shadow.res, shadow.res.Value)
	delta := shadow.res.Value.GetDelta(res.Value)
	log.Printf("shadow[%s:%p]: found delta: `%s`\n", res.StringRef(), &res, delta)
	shadow.pending = append(shadow.pending, Edit{shadow.SessionClock.Clone(), delta})
	shadow.IncSv()
	shadow.res = res
	return nil
}

func (shadow *Shadow) SyncIncoming(edit Edit, store *Store, ctx context) (err error) {
	// Make sure clocks are in sync or recoverable
	log.Printf("shadow[%s:%p]: sync incoming edit: `%v`\n", shadow.res.StringRef(), &shadow.res, edit)
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
		shadow.IncCv()
		return nil
	}
	newres, patches, err := edit.Delta.Apply(shadow.res.Value)
	if err != nil {
		return err
	}
	shadow.res.Value = newres
	shadow.backup = newres
	shadow.IncCv()
	// send patches to store
	for i := range patches {
		err := store.Patch(shadow.res.Ref(), patches[i], ctx)
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
		Backup       json.RawMessage `json:"backup"`
		Pending      []Edit          `json:"pending"`
		SessionClock `json:"clock"`
	}{}
	if err := json.Unmarshal(from, &tmp); err != nil {
		return err
	}
	shadow.res = Resource{Kind: tmp.Res.Kind, ID: tmp.Res.ID}
	shadow.pending = tmp.Pending
	shadow.SessionClock = tmp.SessionClock
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
	}
	return nil
}
