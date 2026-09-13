package api

import (
	"bytes"
	cas "github.com/openabstractions/abstraction-cas/go"
)

// BoundedFileStore implements the existing direct Store contract over local
// named state. Bounds apply to reads, comparison reads and replacement bytes.
// Providers own this adapter; its path is not an application service argument.
type BoundedFileStore struct{ MaxBytes int64 }

var _ Store = BoundedFileStore{}

func (s BoundedFileStore) Read(path string) (Value, error) {
	data, err := cas.ReadLimit(path, s.MaxBytes)
	return Value{Data: data}, err
}
func (s BoundedFileStore) Write(path string, base Value, data []byte) error {
	if data == nil {
		return cas.ErrNoValue
	}
	return cas.ChangeLimit(path, s.MaxBytes, func(current []byte) ([]byte, error) {
		if (current == nil) != (base.Data == nil) || !bytes.Equal(current, base.Data) {
			return nil, cas.ErrMoved
		}
		return data, nil
	})
}
