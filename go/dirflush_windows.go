package cas

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"syscall"
)

var flushDirectoryBuffers = syscall.FlushFileBuffers

func dirFlushUnsupported(err error) bool {
	return errors.Is(err, windows.ERROR_INVALID_FUNCTION) || errors.Is(err, windows.ERROR_NOT_SUPPORTED) || errors.Is(err, windows.ERROR_INVALID_PARAMETER)
}

func syncDir(dir string) (err error) {
	name, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	const fileWriteData = 0x0002
	h, err := syscall.CreateFile(name, fileWriteData|fileReadAttributes,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return &os.PathError{Op: "open", Path: dir, Err: err}
	}
	defer func() {
		if closeErr := syscall.CloseHandle(h); closeErr != nil {
			err = errors.Join(err, &os.PathError{Op: "close", Path: dir, Err: closeErr})
		}
	}()
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &info); err != nil {
		return &os.PathError{Op: "stat", Path: dir, Err: err}
	}
	if info.FileAttributes&syscall.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return &os.PathError{Op: "open", Path: dir, Err: syscall.ENOTDIR}
	}
	if err := flushDirectoryBuffers(h); err != nil && !dirFlushUnsupported(err) {
		return &os.PathError{Op: "flush", Path: dir, Err: err}
	}
	return nil
}
