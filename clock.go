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

type Rollbacker interface {
	Rollback()
}

type VersionsDivergedError struct {
	server Versioned
	client Versioned
}

func (c VersionsDivergedError) Error() string {
	return fmt.Sprintf("client: %v server: %v", c.server, c.client)
}

type SessionClock struct {
	CV int64 `json:"cv"`
	SV int64 `json:"sv"`
	BV int64 `json:"bv,omitempty"`
}

func (clock SessionClock) Clone() SessionClock {
	return SessionClock{clock.CV, clock.SV, clock.BV}
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

func (clock *SessionClock) Checkpoint() {
	clock.BV = clock.SV
}

func (clock *SessionClock) SyncSvWith(other Versioned, rb Rollbacker) error {
	if clock.SV != other.Sv() {
		if clock.BV != other.Sv() {
			return VersionsDivergedError{*clock, other}
		}
		log.Println("sessionclock: SV mismatch, rolling back backup")
		clock.SV = clock.BV
		rb.Rollback()
	}
	return nil
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
		return false, VersionsDivergedError{clock, other}
	}
	return false, nil
}
