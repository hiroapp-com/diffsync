package diffsync

import (
	"encoding/json"
)

type Patch struct {
	origin_sid string
	val        interface{}
}

type Edit struct {
	Clock SessionClock     `json:"clock"`
	Delta *json.RawMessage `json:"delta"`
}
