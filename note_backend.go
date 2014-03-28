package diffsync

import (
	"sync"
)

type NoteMemBackend struct {
	dict map[string]*NoteValue
	sync.RWMutex
}

func NewNoteMemBackend(init map[string]*NoteValue) *NoteMemBackend {
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
		noteval = NewNoteValue("")
	}
	// tbd: should the (blank) resource already be created or can we wait for the
	//      Upsert to happen later?
	return noteval.CloneValue(), nil
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
	if _, ok := note.(*NoteValue); !ok {
		return InvalidValueError{key, note}
	}
	mem.dict[key] = note.CloneValue().(*NoteValue)
	return nil
}

func (mem *NoteMemBackend) Delete(key string) error {
	mem.Lock()
	defer mem.Unlock()
	delete(mem.dict, key)
	return nil
}

func (mem *NoteMemBackend) DumpAll() []Resource {
	res := make([]Resource, 0, len(mem.dict))
	for id, val := range mem.dict {
		res = append(res, Resource{Kind: mem.Kind(), ID: id, Value: val.CloneValue()})
	}
	return res
}
