package diffsync

type Delta interface{}
type Patch interface{}

type Edit struct {
    SessionClock
    delta Delta
}
