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

func (res *Resource) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"kind": res.kind,
		"id":   res.id,
		"val":  res.ResourceValue,
	})
}

func (res *Resource) UnmarshalJSON(from []byte) error {
	vals := make(map[string]interface{})
	json.Unmarshal(from, vals)
	*res = Resource{
		kind: vals["kind"].(string),
		id:   vals["id"].(string),
	}
	switch resval := vals["val"].(type) {
	case *NoteValue:
	case *MetaValue:
		res.ResourceValue = resval
	default:
	}
	return nil
}
