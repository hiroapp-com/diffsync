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
	backend StoreBackend
}

func NewStore(backend StoreBackend) *Store {
	return &Store{backend: backend}
}

func (store *Store) Load(res *Resource) error {
	// todo: send get request via gdata connection
	value, err := store.backend.Get(res.id)
	if err != nil {
		return err
	}
	// for now we can ignore the exists flag. if it's a new note, here we'll return a blank/initialized value
	// which is the desired case (behaviour needs more documentation). Also the patch matchod will easily
	// make use of the same feature
	(*res).ResourceValue = value.CloneValue()
	return nil
}

func (store *Store) Patch(res *Resource, patch Patch) error {
	value, err := store.backend.Get(res.id)
	if err != nil {
		return err
	}
	// for now we can ignore the exists flag. if it's a new note, here we'll return a blank/initialized value
	// which is the desired case (behaviour needs more documentation). Also the patch matchod will easily
	// make use of the same feature
	value.ApplyPatch(patch, notify)
	if err := store.backend.Upsert(res.id, value); err != nil {
		return err
	}
	//test: note in notestore is modified?
	(*res).ResourceValue = value.CloneValue()
	return nil
}
