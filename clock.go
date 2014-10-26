package diffsync

import (
	"log"
	//"encoding/json"
	"fmt"
)

var (
	_ = log.Print
)

type Versioned interface {
	Cv() int64
	Sv() int64
}

type ErrCVDiverged struct {
	client int64
	server int64
}

type ErrSVDiverged struct {
	client int64
	server int64
}

func (e ErrCVDiverged) Error() string {
	return fmt.Sprintf("CV mismatch: %d (client) != %d (server)", e.client, e.server)
}

func (e ErrSVDiverged) Error() string {
	return fmt.Sprintf("SV mismatch: %d (client) != %d (server)", e.client, e.server)
}

type SessionClock struct {
	CV int64 `json:"cv"`
	SV int64 `json:"sv"`
}

func (clock SessionClock) Clone() SessionClock {
	return SessionClock{clock.CV, clock.SV}
}

func (clock SessionClock) Cv() int64 {
	return clock.CV
}
func (clock *SessionClock) IncCv() {
	clock.CV++
}
func (clock SessionClock) Sv() int64 {
	return clock.SV
}
func (clock *SessionClock) IncSv() {
	clock.SV++
}

func (clock SessionClock) Ack(pending Versioned) bool {
	// Return whether clock acks other
	return clock.SV >= pending.Sv()
}
func (clock SessionClock) CheckCV(other Versioned) (is_dupe bool, err error) {
	switch {
	case clock.CV == other.Cv():
		return false, nil
	case clock.CV > other.Cv():
		return true, nil
	case clock.CV < other.Cv():
		return false, ErrCVDiverged{other.Cv(), clock.CV}
	}
	return false, nil
}
