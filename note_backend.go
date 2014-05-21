package diffsync

import (
	"log"
	"sync"
)

var (
	_ = log.Print
)

type NoteMemBackend struct {
	dict map[string]Note
	sync.RWMutex
}

func NewNoteMemBackend(init map[string]Note) *NoteMemBackend {
	return &NoteMemBackend{dict: init}
}

func (mem *NoteMemBackend) Kind() string {
	return "note"
}

// Always returns a value. if no value exists under key, create a blank object
// and return that
func (mem *NoteMemBackend) Get(key string) (ResourceValue, error) {
	mem.RLock()
	defer mem.RUnlock()
	noteval, ok := mem.dict[key]
	if !ok {
		noteval = NewNote("")
	}
	// tbd: should the (blank) resource already be created or can we wait for the
	//      Upsert to happen later?
	return noteval.Clone(), nil
}

func (mem *NoteMemBackend) GetMany(keys []string) ([]ResourceValue, error) {
	result := make([]ResourceValue, len(keys))
	for _, key := range keys {
		tmpval, err := mem.Get(key)
		if err != nil {
			return result, err
		}
		result = append(result, tmpval)
	}
	return result, nil
}

func (mem *NoteMemBackend) Upsert(key string, note ResourceValue) error {
	mem.Lock()
	defer mem.Unlock()
	if _, ok := note.(Note); !ok {
		return InvalidValueError{key, note}
	}
	mem.dict[key] = note.(Note)
	return nil
}

func (mem *NoteMemBackend) Delete(key string) error {
	mem.Lock()
	defer mem.Unlock()
	delete(mem.dict, key)
	return nil
}

func (mem *NoteMemBackend) DumpAll(kind string) []Resource {
	res := make([]Resource, 0, len(mem.dict))
	for id, val := range mem.dict {
		res = append(res, Resource{Kind: kind, ID: id, Value: val.Clone()})
	}
	return res
}
