package diffsync

import (
	"time"
)

type context struct {
	sid string
	uid string
	ts  time.Time
}
