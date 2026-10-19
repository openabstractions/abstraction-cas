//go:build !windows

package cas

import (
	"errors"
	"os"
	"syscall"
)

func dirFlushUnsupported(err error) bool {
	return errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP)
}

func syncDir(dir string) (err error) {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, d.Close()) }()
	info, err := d.Stat()
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return &os.PathError{Op: "open", Path: dir, Err: syscall.ENOTDIR}
	}
	if err := d.Sync(); err != nil && !dirFlushUnsupported(err) {
		return err
	}
	return nil
}
