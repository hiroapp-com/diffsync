package diffsync

import (
	"time"
)

type Context struct {
	sid     string
	uid     string
	ts      time.Time
	store   *Store
	brdcast EventHandler
	Client  EventHandler
}

func NewContext(brdcast EventHandler, store *Store, client EventHandler) Context {
	return Context{
		ts:      time.Now(),
		brdcast: brdcast,
		store:   store,
		Client:  client,
	}
}
