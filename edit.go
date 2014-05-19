package diffsync

//TODO(flo): change signature of Apply back to Patch instead of []Patch

// this means, shadows and doc-stores can now work with this defined object
type Delta interface {
	HasChanges() bool
	Apply(ResourceValue) (ResourceValue, []Patch, error)
}

type Patch interface {
	Apply(ResourceValue, chan<- Event) (ResourceValue, error)
}

type Edit struct {
	Clock SessionClock `json:"clock"`
	Delta Delta        `json:"delta"`
}
