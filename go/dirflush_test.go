package cas

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestSyncDirRequiresExistingDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := SyncDir(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("record"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := SyncDir(path); !errors.Is(err, syscall.ENOTDIR) {
		t.Fatalf("regular file: %v", err)
	}
	if err := SyncDir(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing directory: %v", err)
	}
}
