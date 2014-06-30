package diffsync

// todo: comment about expectations to methods regarding transactionality
// and thread-safety

import (
	"errors"
	"fmt"
	"log"

	"database/sql"
)

var (
	_ = log.Print
)

type ResourceBackend interface {
	Get(string) (ResourceValue, error)
	Patch(string, Patch, *Store, context) error
	CreateEmpty(context) (string, error)
}

type Store struct {
	backends map[string]ResourceBackend
	notify   chan<- Event
	userDB   *sql.DB
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

func NewStore(userDB *sql.DB, notify chan<- Event) *Store {
	return &Store{backends: map[string]ResourceBackend{}, userDB: userDB, notify: notify}
}

func (store *Store) Mount(kind string, backend ResourceBackend) {
	store.backends[kind] = backend
}

func (store *Store) GetOrCreateUser(userRef User) (User, error) {
	if store.userDB == nil {
		return User{}, errors.New("store: user-db not set, cannot query or create users")
	}
	return getOrCreateUser(userRef, store.userDB)
}

func (store *Store) NewResource(kind string, ctx context) (Resource, error) {
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
	log.Printf("resource[%s:%p]: loading data", res.StringRef(), res)
	// todo: send get request via gdata connection
	value, err := store.backends[res.Kind].Get(res.ID)
	if err != nil {
		return err
	}
	res.Value = value
	return nil
}

func (store *Store) Patch(res Resource, patch Patch, ctx context) error {
	return store.backends[res.Kind].Patch(res.ID, patch, store, ctx)
}

func (store *Store) NotifyReset(kind, id string, ctx context) {
	select {
	case store.notify <- Event{Name: "res-reset", Res: Resource{Kind: kind, ID: id}, store: store, ctx: ctx}:
		return
	default:
		log.Printf("store: cannot send `res-reset`, notify channel not writable.\n")
	}
}

func (store *Store) NotifyTaint(kind, id string, ctx context) {
	select {
	case store.notify <- Event{Name: "res-taint", Res: Resource{Kind: kind, ID: id}, store: store, ctx: ctx}:
		return
	default:
		log.Printf("store: cannot send `res-taint`, notify channel not writable.\n")
	}
}

func (err InvalidValueError) Error() string {
	return fmt.Sprintf("Invalid Value %s", err.key)
}

func (err NoExistError) Error() string {
	return fmt.Sprintf("Resource with ID %s does not exist", err.key)
}
