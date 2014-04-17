package diffsync

import (
	"encoding/json"
	"fmt"
)

// this means, shadows and doc-stores can now work with this defined object
type ResourceValue interface {
	ApplyDelta(json.RawMessage) (Patch, error)
	GetDelta(ResourceValue) (json.RawMessage, error)
	ApplyPatch(Patch, chan<- Event) (bool, error)
	CloneValue() ResourceValue
	json.Marshaler
	json.Unmarshaler
	fmt.Stringer
}

// todo(refactore) export fields
type Resource struct {
	Kind  string        `json:"kind"`
	ID    string        `json:"id"`
	Value ResourceValue `json:"val,omitempty"`
}

func NewResource(kind, id string) Resource {
	return Resource{Kind: kind, ID: id, Value: NilValue{}}
}

func (res *Resource) CloneEmpty() Resource {
	return Resource{Kind: res.Kind, ID: res.ID, Value: NilValue{}}
}

func (res *Resource) StringID() string {
	return fmt.Sprintf("%s:%s", res.Kind, res.ID)
}

func (res *Resource) String() string {
	if res.Value == nil {
		return fmt.Sprintf("kind: `%s`, id:`%s`", res.Kind, res.ID)
	}
	return fmt.Sprintf("kind: `%s`, id:`%s`, value:`%s`", res.Kind, res.ID, res.Value.String())
}
