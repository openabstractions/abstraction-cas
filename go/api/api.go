// Package api connects the schema-defined direct API to the native file provider.
package api

import (
	cas "github.com/openabstractions/abstraction-cas/go"
	generated "github.com/openabstractions/abstraction-cas/go/abstraction/cas/api"
)

type Value = generated.Value
type Store = generated.Store

// FileStore is stateless; the existing provider owns locks and file replacement.
type FileStore struct{}

var _ Store = FileStore{}

func (FileStore) Read(path string) (Value, error) {
	data, err := cas.Read(path)
	return Value{Data: data}, err
}
func (FileStore) Write(path string, base Value, data []byte) error {
	return cas.Write(path, base.Data, data)
}
