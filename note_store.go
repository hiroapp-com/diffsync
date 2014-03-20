package diffsync

import (
    "fmt"
    "log"
    "sync"
    "errors"

    DMP "github.com/sergi/go-diff/diffmatchpatch"
)

var (
    dmp = DMP.New()
    _ = log.Print
    noteStore = &NoteStore{notes: map[string]NoteValue{}}
)

type ResourceStore interface {
    LoadInto(*Resource) error
    PatchInto(*Resource, Patch) error
    Put(*Resource) error
    Delete(*Resource) error
}


type NoteStore struct {
    notes map[string]NoteValue
    sync.RWMutex
    // gdata GoogleDatastoreConnection
}

func (store *NoteStore) Load(res *Resource) error {
    if res.kind != "note" {
        return errors.New(fmt.Sprintf("received LoadInto call for unsupported kind `%s`. Can only process `note`s"))
    }
    store.Lock()
    defer store.Unlock()
    // todo: send get request via gdata connection
    note, _ := store.notes[res.id]
    // for now we can ignore the exists flag. if it's a new note, here we'll return a blank/initialized value 
    // which is the desired case (behaviour needs more documentation). Also the patch matchod will easily 
    // make use of the same feature
    (*res).ResourceValue = &note
    return nil
}

func (store *NoteStore) Patch(res *Resource, patch Patch) error {
    if res.kind != "note" {
        return errors.New(fmt.Sprintf("received LoadInto call for unsupported kind `%s`. Can only process `note`s"))
    }
    store.Lock()
    defer store.Unlock()
    // todo: send get request via gdata connection
    note, _ := store.notes[res.id]
    // for now we can ignore the exists flag. if it's a new note, here we'll return a blank/initialized value 
    // which is the desired case (behaviour needs more documentation). Also the patch matchod will easily 
    // make use of the same feature
    notify := make(chan Event)
    note.ApplyPatch(patch, notify)
    (*res).ResourceValue = &note
    return nil
}

func (store *NoteStore) Upsert(res *Resource, patch Patch) error {
    val := res.ResourceValue.(*NoteValue)
    store.notes[res.id] = *val
    return nil
}

func (store *NoteStore) Delete(res *Resource) error {
    delete(store.notes, res.id)
    return nil
}

