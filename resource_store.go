package diffsync

// todo: comment about expectations to methods regarding transactionality
// and thread-safety

import (
	"log"
)

var (
	_ = log.Print
)

type Store struct {
	kind    string
	backend StoreBackend
	notify  chan<- Event
}

func NewStore(kind string, backend StoreBackend, notify chan<- Event) *Store {
	return &Store{kind: kind, backend: backend, notify: notify}
}

func (store *Store) Load(res *Resource) error {
	log.Printf("resource[%s:%p]: loading data", res.StringRef(), res)
	// todo: send get request via gdata connection
	value, err := store.backend.Get(res.ID)
	if err != nil {
		return err
	}
	// for now we can ignore the exists flag. if it's a new note, here we'll return a blank/initialized value
	// which is the desired case (behaviour needs more documentation). Also the patch matchod will easily
	// make use of the same feature
	res.Value = value.Clone()
	return nil
}

func (store *Store) NotifyReset(id string, sid string) {
	select {
	case store.notify <- Event{Name: "res-reset", SID: sid, Res: Resource{Kind: store.kind, ID: id}}:
		return
	default:
		log.Printf("store[%s]: cannot send `res-reset`, notify channel not writable.\n", store.kind)
	}
}

func (store *Store) NotifyTaint(id string, sid string) {
	select {
	case store.notify <- Event{Name: "res-taint", SID: sid, Res: Resource{Kind: store.kind, ID: id}}:
		return
	default:
		log.Printf("store[%s]: cannot send `res-taint`, notify channel not writable.\n", store.kind)
	}
}

func (store *Store) Patch(res *Resource, patch Patcher, sid string) error {
	value, err := store.backend.Get(res.ID)
	if err != nil {
		return err
	}
	newval, err := patch.Patch(value, store.notify)
	if err != nil {
		return err
	}
	if newval.GetDelta(value).HasChanges() {
		if err := store.backend.Upsert(res.ID, newval); err != nil {
			return err
		}
		store.NotifyTaint(res.ID, sid)
	}
	return nil
}
