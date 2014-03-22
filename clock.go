package diffsync

import (
	"encoding/json"
	"fmt"
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
	cv, sv, bv int64
}

func (clock SessionClock) Clone() SessionClock {
	return SessionClock{clock.cv, clock.sv, clock.bv}
}

func (clock SessionClock) Cv() int64 {
	return clock.cv
}
func (clock *SessionClock) IncCv() {
	clock.cv++
}
func (clock SessionClock) Sv() int64 {
	return clock.sv
}
func (clock *SessionClock) IncSv() {
	clock.sv++
}

func (clock *SessionClock) Checkpoint() {
	clock.bv = clock.sv
}

func (clock *SessionClock) SyncSvWith(other Versioned, rb Rollbacker) error {
	if clock.sv != other.Sv() {
		if clock.bv != other.Sv() {
			return VersionsDivergedError{*clock, other}
		}
		clock.sv = clock.bv
		rb.Rollback()
	}
	return nil
}

func (clock SessionClock) Ack(pending Versioned) bool {
	// Return whether clock acks other
	return clock.sv >= pending.Sv()
}
func (clock SessionClock) CheckCV(other Versioned) (is_dupe bool, err error) {
	switch {
	case clock.cv == other.Cv():
		return false, nil
	case clock.cv > other.Cv():
		return true, nil
	case clock.cv < other.Cv():
		return false, VersionsDivergedError{clock, other}
	}
	return false, nil
}

func (clock *SessionClock) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]int64{
		"cv": clock.cv,
		"sv": clock.sv,
		"bv": clock.bv,
	})
}

func (clock *SessionClock) UnmarshalJSON(from []byte) error {
	vals := make(map[string]int64)
	json.Unmarshal(from, vals)
	*session = SessionClock{cv: vals["cv"],
		sv: vals["sv"],
		bv: vals["bv"],
	}
	return nil
}
