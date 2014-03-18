package diffsync

import (
    "encoding/json"
)

// this means, shadows and doc-stores can now work with this defined object
type ResourceValue interface {
	ApplyDelta(Delta, chan<- Event) (Patch, error)
	GetDelta(ResourceValue) (Delta, error)
	ApplyPatch(Patch) error
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
