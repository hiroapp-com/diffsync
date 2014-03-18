package diffsync

import (
    "log"
    "sync"

    DMP "github.com/sergi/go-diff/diffmatchpatch"
)

var (
    dmp = DMP.New()
    _ = log.Print
)


type NoteStore struct {
    notes map[string]string
    sync.RWMutex
}
