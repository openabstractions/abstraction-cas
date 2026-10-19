package cas

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	identity "github.com/openabstractions/abstraction-identity"
)

// The lock holds within one host, so every call that takes it refuses a file
// on a network share with identity's reason and leaves nothing there.
func TestAFileOnANetworkShareIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(shareSpelling(t, dir), "record")
	calls := map[string]func() error{
		"LocalPath": func() error { return LocalPath(path) },
		"Write":     func() error { return Write(path, nil, []byte("1")) },
		"Change":    func() error { return Change(path, increment) },
		"ChangeLimitContext": func() error {
			return ChangeLimitContext(context.Background(), path, 64, increment)
		},
		"Sweep": func() error { _, err := Sweep(path); return err },
		"NewPlacement": func() error {
			_, err := NewPlacement(filepath.Join(filepath.Dir(path), "root"), t.TempDir())
			return err
		},
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, identity.ErrNotLocalPath) {
			t.Errorf("%s on %s: %v, want identity.ErrNotLocalPath", name, path, err)
		}
	}
	// The refusal names the path once, in identity's reason.
	if err := Write(path, nil, []byte("1")); err == nil || strings.Count(err.Error(), path) != 1 {
		t.Errorf("Write on %s: %v, want the path named once", path, err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("refused calls left %v in %s (%v)", entries, dir, err)
	}
}
