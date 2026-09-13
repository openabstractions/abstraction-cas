package cas

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

const (
	accessDenied     syscall.Errno = 5
	sharingViolation syscall.Errno = 32
	exclusiveLock                  = 2
)

var lockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")

func openBoundedRecord(root *os.Root, name string) (*os.File, error) { return root.Open(name) }

func transient(err error) bool {
	return errors.Is(err, sharingViolation) || errors.Is(err, accessDenied)
}

func flock(f *os.File) error {
	var ov syscall.Overlapped
	r, _, e := lockFileEx.Call(f.Fd(), exclusiveLock, 0, 1, 0, uintptr(unsafe.Pointer(&ov)))
	if r == 0 {
		return &os.PathError{Op: "lock", Path: f.Name(), Err: e}
	}
	return nil
}
