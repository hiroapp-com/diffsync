package diffsync

// todo: comment about expectations to methods regarding transactionality
// and thread-safety

import (
	"fmt"
	"log"

	"bitbucket.org/sushimako/hync/comm"
)

var (
	_ = log.Print
)

type ResourceBackend interface {
	Get(string) (ResourceValue, error)
	Patch(string, Patch, *SyncResult, Context) error
	CreateEmpty(Context) (string, error)
}

type Store struct {
	backends    map[string]ResourceBackend
	commHandler comm.Handler
}

type Patch struct {
	Op       string
	Path     string
	Value    interface{}
	OldValue interface{}
}

type NoExistError struct {
	key string
}

type InvalidValueError struct {
	key string
	val interface{}
}

func NewStore(comm comm.Handler) *Store {
	return &Store{backends: map[string]ResourceBackend{}, commHandler: comm}
}

func (store *Store) Mount(kind string, backend ResourceBackend) {
	store.backends[kind] = backend
}

func (store *Store) NewResource(kind string, ctx Context) (Resource, error) {
	res := Resource{Kind: kind}
	// create new Nil Resource in backend
	var err error
	res.ID, err = store.backends[kind].CreateEmpty(ctx)
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
	log.Printf("resource[%s]: loading data", res.StringRef())
	// todo: send get request via gdata connection
	value, err := store.backends[res.Kind].Get(res.ID)
	if err != nil {
		return err
	}
	res.Value = value
	return nil
}

func (store *Store) Patch(res Resource, patch Patch, result *SyncResult, ctx Context) error {
	return store.backends[res.Kind].Patch(res.ID, patch, result, ctx)
}

func (err InvalidValueError) Error() string {
	return fmt.Sprintf("Invalid Value %s", err.key)
}

func (err NoExistError) Error() string {
	return fmt.Sprintf("Resource with ID %s does not exist", err.key)
}
