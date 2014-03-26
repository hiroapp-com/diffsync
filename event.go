package diffsync

import (
	"encoding/json"
)

type Event struct {
	name   string
	sid    string
	tag    string
	data   interface{}
	client chan<- Event
}

func NewEvent(name, sid string, data interface{}, client chan<- Event) Event {
	return Event{
		name:   name,
		sid:    sid,
		data:   data,
		client: client,
	}
}

type ResLoadData struct {
	res  *Resource
	done chan struct{}
}

type SyncData struct {
	res     Resource
	changes []Edit
}

func NewSyncData(kind, id string, changes []Edit) SyncData {
	return SyncData{res: NewResource(kind, id), changes: changes}
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

func (event Event) SID() string {
	return event.sid
}
func (event Event) Name() string {
	return event.name
}
func (event Event) Data() interface{} {
	return event.data
}

func (data SyncData) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"res":     data.res,
		"changes": data.changes,
	})
}
