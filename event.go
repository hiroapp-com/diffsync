package diffsync

// Event defines the main datastructure used for communication between all components
//
// All incoming client messages are of type Event and all the parts
// of the System communicate by sending Event objects via channels.
//
// The Event type lays out all possible properties in a flat way and without using
// interfaces or other late type-checking. Every event-type uses just the fields that
// are needed to proces the event. All other fields are set to their respective nil values
// Luckily, there is a good overlap of "needed fields" between the event-types so far
//
// Empty/nil fields can and should be omitted when serializing to json, to avoid
// unnecessary payload-increase.
//
type Event struct {
	// Name identifies the Event-Type
	//
	// Each event-type has its own set of needed fields and
	// different parts of the infrastructure might respond to
	// them or route them into a receivers direction
	//
	// Currently the following Name's are in use: session-ceate, res-sync, res-reset, res-taint
	Name string `json:"name"`

	// SID contains the session-id which this Event is associated to.
	//
	// Can be used by a client to send any sync requests. Internally, it will also be used
	// by te notifying mechanism. If a session's operation caused any notifications to be
	// broadcast to other sessions/users, the SID will indicate the origin SID
	SID string `json:"sid"`

	// A simple request/response Tag
	//
	// Used to find out if a sync-event was initiated by the client or the server itself
	// Each side keeps their own "tag-library". When initiating a sync-cycle, a tag is
	// generated and the to-sync resource will be tagged in the library with that tag.
	// if the tag receives a response, the resource can be untagged. If a different tag
	// arrives, we know that two sync cycles have been initiated simultaneously by the
	// server and the client
	Tag string `json:"tag, omitempty"`

	// A HiroToken to use for any login and sharing flows
	//
	// session-create events use the Token to authenticate themself.
	Token string `json:"token,omitempty"`

	// The Edit-Queue sent along with any res-sync requests
	Changes []Edit `json:"changes,omitempty"`

	// Res is used to reference or transmit a Resource
	//
	// If an event simply wants to reference something (e.g. res-taint)
	// a simple &Resource{Kind: ..., ID, ...} is enough
	//
	// Every sender should be aware that a receiver might modify
	// the Res.
	Res Resource `json:"res,omitempty"`

	// Session contains a complete "workspace" of a session
	//
	// It's mainly used in the respond to a session-create Event.
	// See SessionData.MarshalJSON for more info about its layout
	Session *Session `json:"session,omitempty"`

	// A channel that wants from now on receive client-responses to this
	// event and any further events for this Event's SID
	//
	// If client is not nil, the Session will remember the channel
	// and whenever it wants to push Events up to the client it tries
	// to send on the last set client.
	// a new incoming Event.client will overwrite also existing (and maybe still
	// living) client.
	client chan<- Event
}

func NewEvent() Event {
	return Event{Changes: []Edit{}, Res: Resource{}, Session: nil}
}
