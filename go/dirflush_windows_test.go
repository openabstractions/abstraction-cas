package cas

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestSyncDirWindowsUnsupportedErrorsAndIOFailures(t *testing.T) {
	old := flushDirectoryBuffers
	t.Cleanup(func() { flushDirectoryBuffers = old })
	dir := t.TempDir()
	for _, code := range []syscall.Errno{windows.ERROR_INVALID_FUNCTION, windows.ERROR_NOT_SUPPORTED, windows.ERROR_INVALID_PARAMETER, windows.ERROR_IO_DEVICE, windows.ERROR_ACCESS_DENIED} {
		t.Run(code.Error(), func(t *testing.T) {
			called := false
			flushDirectoryBuffers = func(syscall.Handle) error { called = true; return &os.PathError{Op: "flush", Err: code} }
			err := SyncDir(dir)
			unsupported := code == windows.ERROR_INVALID_FUNCTION || code == windows.ERROR_NOT_SUPPORTED || code == windows.ERROR_INVALID_PARAMETER
			if !called || (err == nil) != unsupported || !unsupported && !errors.Is(err, code) {
				t.Fatal(code, called, err)
			}
		})
	}
}

func TestAtomicWriteFlushesActualWindowsDirectoryHandle(t *testing.T) {
	old := flushDirectoryBuffers
	t.Cleanup(func() { flushDirectoryBuffers = old })
	calls := 0
	flushDirectoryBuffers = func(h syscall.Handle) error {
		calls++
		var info syscall.ByHandleFileInformation
		if err := syscall.GetFileInformationByHandle(h, &info); err != nil {
			t.Fatal(err)
		}
		if info.FileAttributes&syscall.FILE_ATTRIBUTE_DIRECTORY == 0 {
			t.Fatal("flush received a file handle")
		}
		if err := old(h); err != nil {
			t.Fatalf("actual local directory flush: %v", err)
		}
		return nil
	}
	path := filepath.Join(t.TempDir(), "record")
	if err := Change(path, func([]byte) ([]byte, error) { return []byte("durable record"), nil }); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("directory flush calls: %d", calls)
	}
}
