package diffsync

import (
	"encoding/json"
	"fmt"
)

// this means, shadows and doc-stores can now work with this defined object
type ResourceValue interface {
	ApplyDelta(Delta) (Patch, error)
	GetDelta(ResourceValue) (Delta, error)
	ApplyPatch(Patch, chan<- Event) (bool, error)
	CloneValue() ResourceValue
	json.Marshaler
	json.Unmarshaler
}

type Resource struct {
	kind string
	id   string
	ResourceValue
}

func (res *Resource) CloneEmpty() *Resource {
	return &Resource{kind: res.kind, id: res.id}
}

func (res *Resource) StringId() string {
	return fmt.Sprintf("%s:%s", res.kind, res.id)
}
