package diffsync

import (
	"time"
)

type Context struct {
	sid     string
	uid     string
	ts      time.Time
	store   *Store
	}
}
