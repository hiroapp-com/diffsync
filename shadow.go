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

func NewShadow(res Resource) *Shadow {
	return &Shadow{
		res:          res,
		backup:       res.CloneValue(),
		pending:      []Edit{},
		SessionClock: SessionClock{},
	}
}

func (shadow *Shadow) Rollback() {
	shadow.res.ResourceValue = shadow.backup
	shadow.pending = []Edit{}
}

func (shadow *Shadow) UpdatePending(store *Store) error {
	//send "res-load" to store
	res := shadow.res.CloneEmpty()
	// noteStore needs to be abstracted away to abstract Store
	_ = store.Load(&res)
	delta, err := shadow.res.GetDelta(res.ResourceValue)
	if err != nil {
		return err
	}
	shadow.pending = append(shadow.pending, Edit{shadow.SessionClock.Clone(), delta})
	shadow.IncSv()
	return nil
}

func (shadow *Shadow) SyncIncoming(edit Edit, store *Store) (changed bool, err error) {
	// Make sure clocks are in sync or recoverable
	log.Println(edit)
	log.Println(shadow)
	if err := shadow.SessionClock.SyncSvWith(edit, shadow); err != nil {
		return false, err
	}
	pending := make([]Edit, len(shadow.pending))
	for _, instack := range shadow.pending {
		if !edit.Ack(instack) {
			pending = append(pending, instack)
		}
	}
	if dupe, err := shadow.CheckCV(edit); dupe {
		return false, nil
	} else if err != nil {
		log.Printf("err sync cv")
		return false, err
	}
	patch, err := shadow.res.ApplyDelta(edit.delta)
	shadow.backup = shadow.res.ResourceValue
	if err != nil {
		return false, err
	}
	shadow.IncCv()
	if patch.val == nil {
		// no changes, we're finished
		return false, nil
	}
	// TODO send res-patch down to res_store
	//    newres = {kind: "note", id
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
