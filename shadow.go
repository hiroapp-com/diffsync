package diffsync

import (
	"encoding/json"
	"log"
)

type Shadow struct {
	sid     string
	res     Resource
	backup  ResourceValue
	pending []Edit
	SessionClock
}

func NewShadow(res Resource, sid string) *Shadow {
	return &Shadow{
		sid:          sid,
		res:          res,
		backup:       res.Value.CloneValue(),
		pending:      []Edit{},
		SessionClock: SessionClock{},
	}
}

func (shadow *Shadow) Rollback() {
	shadow.res.Value = shadow.backup
	shadow.pending = []Edit{}
}

func (shadow *Shadow) UpdatePending(store *Store) error {
	res := shadow.res.CloneEmpty()
	log.Printf("shadow[%s:%p]: calculating new delta and upate pending-queue\n", res.StringID(), &res)
	_ = store.Load(&res)
	log.Printf("shadow[%s:%p]: current mastertext: `%s`\n", res.StringID(), &res, res.Value)
	log.Printf("shadow[%s:%p]: current shadowtext: `%s`\n", shadow.res.StringID(), &shadow.res, shadow.res.Value)
	delta, err := shadow.res.Value.GetDelta(res.Value)
	if err != nil {
		return err
	}
	log.Printf("shadow[%s:%p]: found delta: `%s`\n", res.StringID(), &res, delta)
	shadow.pending = append(shadow.pending, Edit{shadow.SessionClock.Clone(), &delta})
	shadow.IncSv()
	shadow.res = res
	return nil
}

func (shadow *Shadow) SyncIncoming(edit Edit, store *Store) (changed bool, err error) {
	// Make sure clocks are in sync or recoverable
	log.Println(edit)
	log.Println(shadow)
	if err := shadow.SessionClock.SyncSvWith(edit.Clock, shadow); err != nil {
		return false, err
	}
	pending := make([]Edit, 0, len(shadow.pending))
	for _, instack := range shadow.pending {
		if !edit.Clock.Ack(instack.Clock) {
			pending = append(pending, instack)
		}
	}
	shadow.pending = pending
	if dupe, err := shadow.CheckCV(edit.Clock); dupe {
		return false, nil
	} else if err != nil {
		log.Printf("err sync cv")
		return false, err
	}
	patch, err := shadow.res.Value.ApplyDelta(*edit.Delta)
	shadow.backup = shadow.res.Value.CloneValue()
	if err != nil {
		return false, err
	}
	shadow.IncCv()
	if patch.val == nil {
		// no changes, we're finished
		return false, nil
	}
	patch.origin_sid = shadow.sid
	return true, store.Patch(&(*shadow).res, patch)
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
	vals := make(map[string]interface{})
	json.Unmarshal(from, vals)
	*shadow = Shadow{
		res:          vals["res"].(Resource),
		backup:       vals["backup"].(ResourceValue),
		pending:      vals["pending"].([]Edit),
		SessionClock: vals["clock"].(SessionClock),
	}
	return nil
}
