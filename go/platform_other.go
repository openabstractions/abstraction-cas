//go:build !windows

package cas

import (
	"errors"
	"os"
	"syscall"
)

// syncDir flushes a directory after a rename in it, so the new entry survives
// a power cut. A file system that cannot sync a directory answers EINVAL or
// ENOTSUP, and the write stands without that guarantee.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOTSUP) {
		return err
	}
	return nil
}

func transient(error) bool { return false }

func flock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX) }

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
