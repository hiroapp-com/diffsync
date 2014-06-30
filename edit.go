package diffsync

type Delta interface {
	HasChanges() bool
	Apply(ResourceValue) (ResourceValue, []Patch, error)
}

type Patch struct {
	Op       string
	Path     string
	Value    interface{}
	OldValue interface{}
}

type Edit struct {
	Clock SessionClock `json:"clock"`
	Delta Delta        `json:"delta"`
}
