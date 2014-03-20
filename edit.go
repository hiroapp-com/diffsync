package diffsync

type Delta interface{}

type Patch struct {
	origin_sid string
	val        interface{}
}

type Edit struct {
	SessionClock
	delta Delta
}
