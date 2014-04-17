package diffsync

import (
	"fmt"
)

type StoreBackend interface {
	Get(string) (ResourceValue, error)
	GetMany([]string) ([]ResourceValue, error)
	Upsert(string, ResourceValue) error
	Delete(string) error
	DumpAll(string) []Resource
}

type InvalidValueError struct {
	key string
	val interface{}
}

func (err InvalidValueError) Error() string {
	return fmt.Sprintf("Invalid Value %s", err.key)
}
