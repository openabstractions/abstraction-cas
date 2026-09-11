package api

import (
	"bytes"
	"errors"
	cas "github.com/openabstractions/abstraction-cas/go"
	"path/filepath"
	"testing"
)

func TestNativeStorePreservesValuesAndErrors(t *testing.T) {
	var store Store = FileStore{}
	path := filepath.Join(t.TempDir(), "value")
	absent, e := store.Read(path)
	if e != nil || absent.Data != nil {
		t.Fatal(absent, e)
	}
	if e = store.Write(path, absent, []byte{}); e != nil {
		t.Fatal(e)
	}
	empty, e := store.Read(path)
	if e != nil || empty.Data == nil || len(empty.Data) != 0 {
		t.Fatal(empty, e)
	}
	data := []byte{0, 255, 128, 10}
	if e = store.Write(path, empty, data); e != nil {
		t.Fatal(e)
	}
	found, e := store.Read(path)
	if e != nil || !bytes.Equal(found.Data, data) {
		t.Fatal(found, e)
	}
	if e = store.Write(path, absent, data); !errors.Is(e, cas.ErrMoved) {
		t.Fatal(e)
	}
	if e = store.Write(path, found, nil); !errors.Is(e, cas.ErrNoValue) {
		t.Fatal(e)
	}
}
