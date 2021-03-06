package diffsync

import (
	"fmt"
)

type Delta interface {
	HasChanges() bool
	Apply(ResourceValue) (ResourceValue, []Patch, error)
}

type ResourceValue interface {
	GetDelta(ResourceValue) Delta
	Clone() ResourceValue
	Empty() ResourceValue
	fmt.Stringer
}

// todo(refactore) export fields
type Resource struct {
	Kind  string        `json:"kind"`
	ID    string        `json:"id"`
	Value ResourceValue `json:"val,omitempty"`
}

func (res *Resource) SameRef(other Resource) bool {
	return res.Kind == other.Kind && res.ID == other.ID
}

func NewResource(kind, id string) Resource {
	return Resource{Kind: kind, ID: id}
}

func (res *Resource) Ref() Resource {
	return Resource{Kind: res.Kind, ID: res.ID}
}

func (res *Resource) StringRef() string {
	return fmt.Sprintf("%s:%s", res.Kind, res.ID)
}

func (res *Resource) String() string {
	if res.Value == nil {
		return fmt.Sprintf("kind: `%s`, id:`%s`", res.Kind, res.ID)
	}
	return fmt.Sprintf("kind: `%s`, id:`%s`, value:`%s`", res.Kind, res.ID, res.Value.String())
}
