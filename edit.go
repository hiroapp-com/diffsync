package diffsync

import (
	"encoding/json"
)

type Delta interface {
	json.Marshaler
}

type Patch struct {
	origin_sid string
	val        interface{}
}

type Edit struct {
	SessionClock
	delta Delta
}

func NewEdit(delta Delta) Edit {
	return Edit{SessionClock{}, delta}
}

func (edit Edit) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"clock": edit.SessionClock,
		"delta": edit.delta,
	})
}
