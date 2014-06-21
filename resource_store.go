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
	kind     string
	backends map[string]StoreBackend
	backend  StoreBackend
	notify   chan<- Event
}

func (store *Store) OpenNew(kind string) *Store {
	return &Store{kind: kind, backends: store.backends, backend: store.backends[kind], notify: store.notify}
}

func NewStore(kind string, backends map[string]StoreBackend, notify chan<- Event) *Store {
	return &Store{kind: kind, backends: backends, backend: backends[kind], notify: notify}
}

func (store *Store) CreateEmpty() (Resource, error) {
	res := Resource{Kind: store.kind}
	// create new Nil Resource in backend
	var err error
	res.ID, err = store.backend.Insert(nil)
	if err != nil {
		return Resource{}, err
	}
	// and now retrieve it
	if err = store.Load(&res); err != nil {
		return Resource{}, err
	}
	return res, nil
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

func (store *Store) LoadOrCreate(res *Resource) (created bool, err error) {
	err = store.Load(res)
	if _, ok := err.(NoExistError); ok {
		newID, err := store.backend.Insert(res.Value)
		if err != nil {
			return false, err
		}
		res.ID = newID
		created = true
	}
	return
}

func (store *Store) NotifyReset(id string) {
	select {
	case store.notify <- Event{Name: "res-reset", SID: "", Res: Resource{Kind: store.kind, ID: id}}:
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

func (store *Store) Patch(res *Resource, patch Patcher, sid string) (bool, error) {
	value, err := store.backend.Get(res.ID)
	if err != nil {
		return false, err
	}
	newval, err := patch.Patch(value, store)
	if err != nil {
		return false, err
	}
	if newval.GetDelta(value).HasChanges() {
		if err := store.backend.Upsert(res.ID, newval); err != nil {
			return true, err
		}
		return true, nil
	}
	return false, nil
}
