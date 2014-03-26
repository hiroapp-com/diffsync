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
	fmt.Stringer
}

// todo(refactore) export fields
type Resource struct {
	kind string
	id   string
	ResourceValue
}

func NewResource(kind, id string) Resource {
	return Resource{kind: kind, id: id, ResourceValue: NilValue{}}
}

func (res *Resource) CloneEmpty() Resource {
	return Resource{kind: res.kind, id: res.id, ResourceValue: NilValue{}}
}

func (res *Resource) StringID() string {
	return fmt.Sprintf("%s:%s", res.kind, res.id)
}

func (res *Resource) String() string {
	if res.ResourceValue != nil {
		return fmt.Sprintf("kind: `%s`, id:`%s`", res.kind, res.id)
	}
	return fmt.Sprintf("kind: `%s`, id:`%s`, value:`%s`", res.kind, res.id, res.ResourceValue.String())
}

func (res Resource) MarshalJSON() ([]byte, error) {
	data := map[string]interface{}{
		"kind": res.kind,
		"id":   res.id,
	}
	if _, is_nil := res.ResourceValue.(NilValue); !is_nil {
		data["val"] = res.ResourceValue
	}
	return json.Marshal(data)
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
