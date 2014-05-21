package diffsync

//TODO(flo): change signature of Apply back to Patch instead of []Patch
//TODO(flo): change Patch to Patcher and method to Patch()

// this means, shadows and doc-stores can now work with this defined object
type Delta interface {
	HasChanges() bool
	Apply(ResourceValue) (ResourceValue, []Patcher, error)
}

type Patcher interface {
	Patch(ResourceValue, chan<- Event) (ResourceValue, error)
}

type Edit struct {
	Clock SessionClock `json:"clock"`
	Delta Delta        `json:"delta"`
}
