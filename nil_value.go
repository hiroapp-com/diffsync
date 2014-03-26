package diffsync

import (
	"encoding/json"
	"fmt"
)

// A valid ResourceValue that can be used to indicate a nil value 
// If a Resource's ResourceValue is of type NilValue, it can only 
// be used as a "reference" object.
type NilValue struct{}

func (note NilValue) ApplyDelta(delta Delta) (Patch, error) {
	return Patch{}, fmt.Errorf("cannot apply delta to NilValue")
}

func (note NilValue) GetDelta(latest ResourceValue) (Delta, error) {
	return nil, fmt.Errorf("cannot get Delta from NilValue")
}

func (note NilValue) ApplyPatch(patch Patch, notify chan<- Event) (changed bool, err error) {
	return false, fmt.Errorf("cannot patch a NilValue")
}

func (nv NilValue) CloneValue() ResourceValue {
	return NilValue{}
}

func (note NilValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(nil)
}

func (note NilValue) UnmarshalJSON(from []byte) error {
	return nil
}

func (note NilValue) String() string {
	return "nil"
}
