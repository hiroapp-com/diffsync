package diffsync

type Event struct {
	name   string
	sid    string
	tag    string
	data   interface{}
	client chan<- Event
}

type ResLoadData struct {
	res  *Resource
	done chan struct{}
}

type SyncData struct {
	res     Resource
	changes []Edit
}

func NewResLoadEvent(res *Resource) (Event, chan struct{}) {
	// BIG FAT NOTE: the receiver of this event is expected to modify
	// the data in-place. i.e. it will load the current version of
	// given resource and write it to the res object
	// The receiver will notify the sender that the write has finished
	// by closing the donne channel
	done := make(chan struct{})
	return Event{name: "res-load", data: ResLoadData{res, done}}, done
}
