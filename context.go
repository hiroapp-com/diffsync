package diffsync

import (
	"time"
)

type context struct {
	sid       string
	token     string
	timestamp time.Time
}
