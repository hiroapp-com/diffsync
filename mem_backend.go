package diffsync

import (
	"sync"
)

type MemBackend struct {
	NilValue func() ResourceValue
	Dict     map[string]ResourceValue
	sync.RWMutex
}

func (mem *MemBackend) GenID() string {
	return sid_generate()
}

func NewMemBackend(nilValueFunc func() ResourceValue) *MemBackend {
	return &MemBackend{NilValue: nilValueFunc, Dict: make(map[string]ResourceValue)}
}

// Always returns a value. if no value exists under key, create a blank object
// and return that
func (mem *MemBackend) Get(key string) (ResourceValue, error) {
	mem.RLock()
	defer mem.RUnlock()
	val, ok := mem.Dict[key]
	if !ok {
		val = mem.NilValue()
	}
	// tbd: should the (blank) resource already be created or can we wait for the
	//      Upsert to happen later?
	return val.Clone(), nil
}

func (mem *MemBackend) GetMany(keys []string) ([]ResourceValue, error) {
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

func (mem *MemBackend) Insert(val ResourceValue) (string, error) {
	mem.Lock()
	defer mem.Unlock()
	key := mem.GenID()
	mem.Dict[key] = val.Clone()
	return key, nil
}

func (mem *MemBackend) Upsert(key string, val ResourceValue) error {
	mem.Lock()
	defer mem.Unlock()
	mem.Dict[key] = val.Clone()
	return nil
}

func (mem *MemBackend) Delete(key string) error {
	mem.Lock()
	defer mem.Unlock()
	delete(mem.Dict, key)
	return nil
}

func (mem *MemBackend) DumpAll(kind string) []Resource {
	res := make([]Resource, 0, len(mem.Dict))
	for id, val := range mem.Dict {
		res = append(res, Resource{Kind: kind, ID: id, Value: val.Clone()})
	}
	return res
}
