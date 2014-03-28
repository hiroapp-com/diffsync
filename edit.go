package diffsync

import ()

type Delta interface{}

type Patch struct {
	origin_sid string
	val        interface{}
}

type Edit struct {
	Clock SessionClock `json:"clock"`
	Delta Delta        `json:"delta"`
}

func NewEdit(delta Delta) Edit {
	return Edit{SessionClock{}, delta}
}
