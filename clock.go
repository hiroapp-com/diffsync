package diffsync

import (
	"fmt"
	"log"
)

var (
	_ = log.Print
)

type Clock struct {
	CV int64 `json:"cv"`
	SV int64 `json:"sv"`
}

type ErrCVDiverged struct {
	client int64
	server int64
}

type ErrSVDiverged struct {
	client int64
	server int64
}

func (e ErrCVDiverged) Error() string {
	return fmt.Sprintf("CV mismatch: %d (client) != %d (server)", e.client, e.server)
}

func (e ErrSVDiverged) Error() string {
	return fmt.Sprintf("SV mismatch: %d (client) != %d (server)", e.client, e.server)
}

func (c Clock) Clone() Clock {
	return c
}
