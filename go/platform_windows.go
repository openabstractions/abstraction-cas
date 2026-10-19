package cas

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"github.com/openabstractions/abstraction-identity/fileid"
	"golang.org/x/sys/windows"
)

// The placement chooses new mirror names from an already physical remainder.
// This is an input mode for fileid, which still resolves/projects their paths.
func placementInput(path string) string {
	if len(filepath.VolumeName(path)) == 2 {
		return `\\?\` + path
	}
	return path
}

// renameFile retains Go's native replace/POSIX semantics without Root's
// GetFullPathName normalization of a verbatim component. Both parent handles
// come from fileid's physical walk; the native calls receive only leaf names.
func renameFile(tmp, path string) error {
	return renameFileChecked(tmp, path, fileid.FileID{}, nil, nil)
}

// A checked provider replacement validates the actual rename handle while its
// read-only sharing excludes writes and deletion by another handle.
func renameFileChecked(tmp, path string, admittedParent fileid.FileID, ctx context.Context, check func(*os.File) error) (err error) {
	from, err := leafOf(tmp, false)
	if err != nil {
		return err
	}
	to, err := leafOf(path, false)
	if err != nil {
		return err
	}
	source, err := fileid.Open(context.Background(), fileid.Self, from.Folder().Path(), fileid.Options{})
	if err != nil {
		return err
	}
	defer source.Close()
	if !source.FileID().Equal(from.Folder()) {
		return ErrMoved
	}
	target, err := fileid.Open(context.Background(), fileid.Self, to.Folder().Path(), fileid.Options{})
	if err != nil {
		return err
	}
	defer target.Close()
	if !target.FileID().Equal(to.Folder()) {
		return ErrMoved
	}
	if check != nil && (!source.FileID().Equal(admittedParent) || !target.FileID().Equal(admittedParent)) {
		return ErrMoved
	}
	name, err := windows.NewNTUnicodeString(from.Name())
	if err != nil {
		return err
	}
	oa := windows.OBJECT_ATTRIBUTES{RootDirectory: windows.Handle(source.File().Fd()), ObjectName: name}
	oa.Length = uint32(unsafe.Sizeof(oa))
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	access, share := uint32(windows.DELETE|windows.SYNCHRONIZE), uint32(windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE)
	if check != nil {
		access |= windows.GENERIC_READ
		share = windows.FILE_SHARE_READ
	}
	err = windows.NtCreateFile(&handle, access, &oa, &status, nil, 0,
		share, windows.FILE_OPEN,
		windows.FILE_OPEN_REPARSE_POINT|windows.FILE_OPEN_FOR_BACKUP_INTENT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	if err != nil {
		return renameError(tmp, path, err)
	}
	renamed := false
	if check == nil {
		defer windows.CloseHandle(handle)
	} else {
		file := os.NewFile(uintptr(handle), tmp)
		defer func() {
			if closeErr := file.Close(); closeErr != nil {
				err = errors.Join(err, closeErr)
				if renamed {
					err = &afterRenameError{err}
				}
			}
		}()
		if err := check(file); err != nil {
			return err
		}
	}
	wide, err := windows.UTF16FromString(to.Name())
	if err != nil {
		return err
	}
	info := struct {
		Flags  uint32
		Root   windows.Handle
		Length uint32
		Name   [windows.MAX_LONG_PATH]uint16
	}{Flags: windows.FILE_RENAME_REPLACE_IF_EXISTS | windows.FILE_RENAME_POSIX_SEMANTICS,
		Root: windows.Handle(target.File().Fd()), Length: uint32((len(wide) - 1) * 2)}
	if len(wide) > len(info.Name) {
		return syscall.EINVAL
	}
	copy(info.Name[:], wide)
	// Match Go's existing fixed-buffer ABI, including the structure's tail
	// alignment for a one-character destination name.
	size := uint32(unsafe.Sizeof(info))
	const fileRenameInformationEx = 65
	if check != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	err = windows.NtSetInformationFile(handle, &status, (*byte)(unsafe.Pointer(&info)), size, fileRenameInformationEx)
	if err != nil {
		// Go's existing fallback for filesystems without POSIX/extended rename.
		info.Flags = windows.FILE_RENAME_REPLACE_IF_EXISTS
		if check != nil {
			if cancelled := ctx.Err(); cancelled != nil {
				return errors.Join(renameError(tmp, path, err), cancelled)
			}
		}
		err = windows.NtSetInformationFile(handle, &status, (*byte)(unsafe.Pointer(&info)), size, windows.FileRenameInformation)
	}
	renamed = err == nil
	return renameError(tmp, path, err)
}

type afterRenameError struct{ error }

func (e *afterRenameError) Unwrap() error { return e.error }

func renameError(from, to string, err error) error {
	if err == nil {
		return nil
	}
	if status, ok := err.(windows.NTStatus); ok {
		err = status.Errno()
	}
	return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
}

const (
	accessDenied       syscall.Errno = 5
	sharingViolation   syscall.Errno = 32
	lockViolation      syscall.Errno = 33
	failImmediately                  = 1
	exclusiveLock                    = 2
	fileReadAttributes               = 0x80
)

// volumeOf is the volume serial number of the directory's volume.
func volumeOf(dir string) (uint64, error) {
	name, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	h, err := syscall.CreateFile(name, fileReadAttributes, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return 0, &os.PathError{Op: "open", Path: dir, Err: err}
	}
	defer syscall.CloseHandle(h)
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &info); err != nil {
		return 0, &os.PathError{Op: "volume", Path: dir, Err: err}
	}
	return uint64(info.VolumeSerialNumber), nil
}

var (
	kernel32   = windows.NewLazySystemDLL("kernel32.dll")
	lockFileEx = kernel32.NewProc("LockFileEx")
)

func openBoundedRecord(root *os.Root, name string) (*os.File, error) { return root.Open(name) }

func transient(err error) bool {
	var committed *afterRenameError
	if errors.As(err, &committed) {
		return false
	}
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

func tryFlock(f *os.File) (bool, error) { return tryFlockMode(f, false) }

func tryFlockMode(f *os.File, shared bool) (bool, error) {
	var ov syscall.Overlapped
	flags := uint32(failImmediately | exclusiveLock)
	if shared {
		flags = failImmediately
	}
	r, _, e := lockFileEx.Call(f.Fd(), uintptr(flags), 0, 1, 0, uintptr(unsafe.Pointer(&ov)))
	if r != 0 {
		return true, nil
	}
	if errors.Is(e, lockViolation) {
		return false, nil
	}
	return false, &os.PathError{Op: "lock", Path: f.Name(), Err: e}
}
