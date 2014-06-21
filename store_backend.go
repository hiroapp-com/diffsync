package diffsync

import (
	"fmt"
)

type StoreBackend interface {
	Get(string) (ResourceValue, error)
	GetMany([]string) ([]ResourceValue, error)
	Upsert(string, ResourceValue) error
	Insert(ResourceValue) (string, error)
	Delete(string) error
	DumpAll(string) []Resource
	GenID() string
}

type NoExistError struct {
	key string
}
type InvalidValueError struct {
	key string
	val interface{}
}

func (err InvalidValueError) Error() string {
	return fmt.Sprintf("Invalid Value %s", err.key)
}

func (err NoExistError) Error() string {
	return fmt.Sprintf("Resource with ID %s does not exist", err.key)
}
