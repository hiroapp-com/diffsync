package diffsync

import (
	"fmt"
)

type ResourceValue interface {
	GetDelta(ResourceValue) Delta
	Clone() ResourceValue
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
