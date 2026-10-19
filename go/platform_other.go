//go:build !windows

package cas

import (
	"errors"
	"os"
	"syscall"
)

func renameFile(tmp, path string) error { return renameRoot(tmp, path) }

func placementInput(path string) string { return path }

func transient(error) bool { return false }

func flock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX) }

func tryFlock(f *os.File) (bool, error) { return tryFlockMode(f, false) }

func tryFlockMode(f *os.File, shared bool) (bool, error) {
	mode := syscall.LOCK_EX
	if shared {
		mode = syscall.LOCK_SH
	}
	err := syscall.Flock(int(f.Fd()), mode|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return false, nil
	}
	return false, err
}

// volumeOf is the device number of the directory's file system.
func volumeOf(dir string) (uint64, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(dir, &st); err != nil {
		return 0, &os.PathError{Op: "stat", Path: dir, Err: err}
	}
	return uint64(st.Dev), nil
}

// Opening a FIFO for reading blocks before Stat can reject it. Nonblocking
// open preserves regular-file behavior and lets the handle type be checked.
func openBoundedRecord(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
