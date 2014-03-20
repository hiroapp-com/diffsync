package diffsync

// todo: comment about expectations to methods regarding transactionality
// and thread-safety


type ResourceStore interface {
	Load(*Resource) error
	Patch(*Resource, Patch) error
	Upsert(*Resource, Patch) error
	Delete(*Resource) error
}
