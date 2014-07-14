package diffsync

// todo: comment about expectations to methods regarding transactionality
// and thread-safety

import (
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
	comm     chan<- CommRequest
	notify   chan<- Event
	userDB   *sql.DB
}

type CommRequest struct {
	uid  string
	kind string
	data map[string]string
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

func NewStore(userDB *sql.DB, notify chan<- Event, comm chan<- CommRequest) *Store {
	return &Store{backends: map[string]ResourceBackend{}, userDB: userDB, notify: notify, comm: comm}
}

func (store *Store) Mount(kind string, backend ResourceBackend) {
	store.backends[kind] = backend
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

func (store *Store) SendInvitation(user User, nid string) {
	//TODO find better place for this method
	txn, err := store.userDB.Begin() // TODO(flo) rename userDB > db OR find better place for this
	if err != nil {
		// cannot process right now
		return
	}
	token, hashed := generateToken()
	commReq := CommRequest{uid: user.UID, data: map[string]string{"token": token}}
	switch {
	case user.Phone != "":
		// TODO(flo) do we want to insert user.Phone into the token already?
		commReq.kind = "phone-invite"
		_, err = txn.Exec("INSERT INTO tokens (token, kind, uid, nid) VALUES (?, 'share-phone', ?, ?)", hashed, user.UID, nid)
		if err != nil {
			txn.Rollback()
			return
		}
	case user.Email != "":
		commReq.kind = "email-invite"
		_, err = txn.Exec("INSERT INTO tokens (token, kind, uid, nid) VALUES (?, 'share-email', ?, ?)", hashed, user.UID, nid)
		if err != nil {
			txn.Rollback()
			return
		}
	default:
		txn.Rollback()
		return
	}
	select {
	case store.comm <- commReq:
	default:
		txn.Rollback()
		return
	}
	txn.Commit()
}

func (err InvalidValueError) Error() string {
	return fmt.Sprintf("Invalid Value %s", err.key)
}

func (err NoExistError) Error() string {
	return fmt.Sprintf("Resource with ID %s does not exist", err.key)
}
