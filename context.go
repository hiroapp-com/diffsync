package diffsync

import (
	"time"
)

type Context struct {
	sid    string
	uid    string
	ts     time.Time
	store  *Store
	Router EventHandler
	Client EventHandler
}

func NewContext(router EventHandler, store *Store, client EventHandler) Context {
	return Context{
		ts:     time.Now(),
		store:  store,
		Router: router,
		Client: client,
	}
}
