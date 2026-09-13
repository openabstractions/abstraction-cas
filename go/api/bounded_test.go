package api

import (
	"bytes"
	"errors"
	cas "github.com/openabstractions/abstraction-cas/go"
	"os"
	"path/filepath"
	"testing"
)

func TestBoundedStorePresenceAndAtomicRefusal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	var store Store = BoundedFileStore{MaxBytes: 4}
	absent, e := store.Read(path)
	if e != nil || absent.Data != nil {
		t.Fatal(absent, e)
	}
	if e = store.Write(path, absent, []byte{}); e != nil {
		t.Fatal(e)
	}
	empty, e := store.Read(path)
	if e != nil || empty.Data == nil {
		t.Fatal(empty, e)
	}
	if e = store.Write(path, absent, []byte("x")); !errors.Is(e, cas.ErrMoved) {
		t.Fatal(e)
	}
	if e = store.Write(path, empty, []byte("oversized")); !errors.Is(e, cas.ErrTooLarge) {
		t.Fatal(e)
	}
	got, e := os.ReadFile(path)
	if e != nil || len(got) != 0 {
		t.Fatal(got, e)
	}
	if e = store.Write(path, empty, []byte{0, 255}); e != nil {
		t.Fatal(e)
	}
	reopened, e := (BoundedFileStore{MaxBytes: 4}).Read(path)
	if e != nil || !bytes.Equal(reopened.Data, []byte{0, 255}) {
		t.Fatal(reopened, e)
	}
	if e = store.Write(path, reopened, nil); !errors.Is(e, cas.ErrNoValue) {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, []byte("too large"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = store.Read(path); !errors.Is(e, cas.ErrTooLarge) {
		t.Fatal(e)
	}
	if e = store.Write(path, reopened, []byte("x")); !errors.Is(e, cas.ErrTooLarge) {
		t.Fatal(e)
	}
	got, _ = os.ReadFile(path)
	if string(got) != "too large" {
		t.Fatal("refusal changed bytes")
	}
}
