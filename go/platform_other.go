//go:build !windows

package cas

import (
	"os"
	"syscall"
)

func transient(error) bool { return false }

func flock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX) }
