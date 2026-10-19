//go:build !windows

package cas

import (
	"os"
	"syscall"
	"testing"
)

func TestSyncDirPOSIXUnsupportedErrors(t *testing.T) {
	for _, code := range []syscall.Errno{syscall.EINVAL, syscall.ENOTSUP} {
		if !dirFlushUnsupported(&os.PathError{Op: "sync", Err: code}) {
			t.Fatalf("unsupported: %v", code)
		}
	}
	for _, code := range []syscall.Errno{syscall.EIO, syscall.EACCES, syscall.ENOENT} {
		if dirFlushUnsupported(&os.PathError{Op: "sync", Err: code}) {
			t.Fatalf("real failure ignored: %v", code)
		}
	}
}
