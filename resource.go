package diffsync

// TODO(flo): Rename CloneValue to Clone()
// TODO(flo): Rename CloneEmpty to Ref()
// TODO(flo): Rename StringID to StringRef()

import (
	"encoding/json"
	"fmt"
)

type ResourceValue interface {
	GetDelta(ResourceValue) Delta
	CloneValue() ResourceValue
	json.Marshaler
	fmt.Stringer
}

// todo(refactore) export fields
type Resource struct {
	Kind  string        `json:"kind"`
	ID    string        `json:"id"`
	Value ResourceValue `json:"val,omitempty"`
}

func NewResource(kind, id string) Resource {
	return Resource{Kind: kind, ID: id}
}

func (res *Resource) CloneEmpty() Resource {
	return Resource{Kind: res.Kind, ID: res.ID}
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
