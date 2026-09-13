//go:build !windows

package cas

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestBoundedRecordRefusesFIFOWithoutWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := ReadLimit(path, 1024); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted as a bounded record")
		}
	case <-time.After(time.Second):
		// Unblock the old implementation so the regression leaves no goroutine.
		f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			f.Close()
			<-done
		}
		t.Fatal("bounded read blocked waiting for a FIFO writer")
	}
}
