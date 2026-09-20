package cas

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

var setFileInformationByHandle = syscall.NewLazyDLL("kernel32.dll").NewProc("SetFileInformationByHandle")

// A read that meets a file whose deletion is pending waits for the name to
// settle. Opening a delete-pending file answers access denied, the state a
// rename briefly leaves behind; a reader that failed on it died in mixed.py.
func TestAReadWaitsOutAPendingDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "n😀-状態")
	if err := Write(path, nil, []byte("before")); err != nil {
		t.Fatal(err)
	}
	release := holdDeletePending(t, path)
	done := make(chan struct{})
	go func() {
		time.Sleep(30 * time.Millisecond)
		release()
		close(done)
	}()
	b, err := Read(path)
	<-done
	if err != nil || b != nil {
		t.Fatalf("Read = %q, %v; want the name gone once the delete settles", b, err)
	}
}

// holdDeletePending marks path delete-pending through a handle it keeps open.
// Opening the name answers access denied until release closes the handle.
func holdDeletePending(t *testing.T, path string) (release func()) {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	const deleteAccess = 0x00010000
	h, err := syscall.CreateFile(name, deleteAccess|syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	const fileDispositionInfo = 4
	disposition := byte(1)
	if r, _, e := setFileInformationByHandle.Call(uintptr(h), fileDispositionInfo, uintptr(unsafe.Pointer(&disposition)), 1); r == 0 {
		syscall.CloseHandle(h)
		t.Fatalf("mark delete pending: %v", e)
	}
	if f, err := os.Open(path); err == nil {
		f.Close()
		syscall.CloseHandle(h)
		t.Fatal("a delete-pending file opened; this test needs the access-denied state")
	} else if !errors.Is(err, accessDenied) {
		syscall.CloseHandle(h)
		t.Fatalf("a delete-pending file answered %v, want access denied", err)
	}
	return func() { syscall.CloseHandle(h) }
}
