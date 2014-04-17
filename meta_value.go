package diffsync

import (
	"encoding/json"
	"errors"
	"time"
)

type Collaborator struct {
	UID            string
	CursorPosition int64
	LastSeen       *time.Time
}

type MetaValue struct {
	Title         string
	CreatedAt     time.Time
	LastUpdateAt  time.Time
	LastUpdateBy  string
	Collaborators []Collaborator
}

//note maybe make notify a global chan
func (note *MetaValue) ApplyDelta(delta json.RawMessage) (Patch, error) {
	return Patch{}, errors.New("Not implemented")
}

// maybe notify should be a global chan
func (note *MetaValue) ApplyPatch(patch Patch, notify chan<- Event) (changed bool, err error) {
	return false, errors.New("Not implemented")
}

func (note *MetaValue) GetDelta(other ResourceValue) (json.RawMessage, error) {
	return []byte{}, errors.New("Not implemented")
}

func (note *MetaValue) MarshalJSON() ([]byte, error) {
	return []byte{}, nil

}

func (meta *MetaValue) String() string {
	return ""
}

func (note *MetaValue) UnmarshalJSON(from []byte) error {
	return errors.New("Not implemented")
}

func (meta *MetaValue) CloneValue() ResourceValue {
	x := *meta
	return &x
}
