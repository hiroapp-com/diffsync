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
