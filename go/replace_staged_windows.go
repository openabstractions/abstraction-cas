package cas

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"

	"github.com/openabstractions/abstraction-identity/fileid"
	"golang.org/x/sys/windows"
)

// ReplaceStagedContext is a Windows provider composition utility. The caller
// supplies a separately admitted, flushed staging file and its identity/content
// pin. It replaces the target entry without reading the previous target value.
// The stage must be a plain single-link file in the target's physical folder.
// The canonical target lock/locality and native rename/durability owners apply.
// Cancellation before rename leaves the target unchanged; an error after rename
// (including close or directory sync) requires rereading the target, not rollback.
// It neither deletes a refused stage nor transfers ownership of caller handles.
func ReplaceStagedContext(ctx context.Context, target, staged string, expected fileid.FileID, expectedSHA256 [32]byte, expectedBytes int64) error {
	if expected.IsZero() || expected.IsDir() || expected.Links() != 1 || expectedBytes < 0 || expectedBytes == math.MaxInt64 {
		return errors.New("cas: invalid staged replacement pin")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := LocalPath(staged); err != nil {
		return err
	}
	pl, err := beside(target)
	if err != nil {
		return err
	}
	stage, err := leafOf(staged, false)
	if err != nil {
		return err
	}
	to, err := leafOf(pl.target, false)
	if err != nil {
		return err
	}
	if !stage.Folder().Equal(to.Folder()) || stage.Name() == to.Name() || !stagedName(stage.Name(), to.Name()+".") {
		return errors.New("cas: staged replacement must be a private temporary beside the target")
	}
	if beforeStagedLock != nil {
		beforeStagedLock()
	}
	unlock, err := lockAtContext(ctx, pl)
	if err != nil {
		return err
	}
	defer unlock()
	check := func(f *os.File) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		key, err := fileid.FileKeyOf(f)
		if err != nil {
			return err
		}
		if key != expected.Key() {
			return ErrMoved
		}
		var info windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
			return err
		}
		if info.NumberOfLinks != 1 || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
			return ErrMoved
		}
		stat, err := f.Stat()
		if err != nil {
			return err
		}
		if !stat.Mode().IsRegular() || stat.Size() != expectedBytes {
			return ErrMoved
		}
		hash := sha256.New()
		n, err := io.Copy(hash, io.LimitReader(stagedReader{ctx, f}, expectedBytes+1))
		if err != nil {
			return err
		}
		if n != expectedBytes || string(hash.Sum(nil)) != string(expectedSHA256[:]) {
			return ErrMoved
		}
		return ctx.Err()
	}
	if err := retry(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return renameFileChecked(stage.Path(), pl.target, to.Folder(), ctx, check)
	}); err != nil {
		return err
	}
	return syncParent(filepath.Dir(pl.target))
}

// The controlled admission/lock race uses the production replacement owner.
var beforeStagedLock func()

type stagedReader struct {
	ctx  context.Context
	file *os.File
}

func (r stagedReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.file.Read(p)
}
