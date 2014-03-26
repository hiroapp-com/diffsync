package diffsync

import (
	"errors"
	"time"
)

type MetaValue struct {
	title         string
	updated_at    *time.Time
	updated_by    string
	collaborators []string
	seen_by       []string
}

//note maybe make notify a global chan
func (note *MetaValue) ApplyDelta(delta Delta) (Patch, error) {
	return Patch{}, errors.New("Not implemented")
}

// maybe notify should be a global chan
func (note *MetaValue) ApplyPatch(patch Patch, notify chan<- Event) (changed bool, err error) {
	return false, errors.New("Not implemented")
}

func (note *MetaValue) GetDelta(other ResourceValue) (Delta, error) {
	return NewNoteValue(""), errors.New("Not implemented")
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
