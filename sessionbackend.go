package diffsync

import (
	"fmt"
	"log"
	"sync"
)

const (
	SESSION_NOTEXIST = iota
	SESSION_EXPIRED
	SESSION_REVOKED
)

type SessionBackend interface {
	Get(string) (*Session, error)
	GetUID(string) (string, error)
	Save(*Session) error
	Delete(string) error
	Release(*Session)
}

type InvalidSessionId struct {
	sid    string
	reason int
}

func (err InvalidSessionId) Error() string {
    return fmt.Sprintf("invalid session-id: %s", err.sid)
}

