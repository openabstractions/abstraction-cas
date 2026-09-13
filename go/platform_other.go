//go:build !windows

package cas

import (
	"os"
	"syscall"
)

func transient(error) bool { return false }

func flock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX) }

// Opening a FIFO for reading blocks before Stat can reject it. Nonblocking
// open preserves regular-file behavior and lets the handle type be checked.
func openBoundedRecord(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
