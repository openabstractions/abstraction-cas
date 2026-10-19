package cas

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	identity "github.com/openabstractions/abstraction-identity"
	"github.com/openabstractions/abstraction-identity/fileid"
)

var ErrMoved = errors.New("cas: the file changed since it was read")

var ErrNoValue = errors.New("cas: no value to write; missing and empty are different values")

func Read(path string) ([]byte, error) { return readLimit(path, 0) }

// ErrTooLarge refuses provider records beyond an explicitly configured bound.
var ErrTooLarge = errors.New("cas: record exceeds read limit")

// ReadLimit is a provider utility with the same missing-file semantics as Read.
func ReadLimit(path string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 || maxBytes == math.MaxInt64 {
		return nil, errors.New("cas: invalid read limit")
	}
	return readLimit(path, maxBytes)
}

// ErrRefused reports a file whose open kept answering access denied or a
// sharing violation until the caller's context ended. The error also wraps the
// context's error and the last open error.
var ErrRefused = errors.New("cas: the file refused every open until the caller's deadline")

// ReadContext is Read with its retry of a denied open bounded by ctx. A writer's
// replace denies an open for an instant, and a file denied for good is denied
// on every retry; ctx decides how long the read waits before ErrRefused.
func ReadContext(ctx context.Context, path string) ([]byte, error) {
	return readLimitContext(ctx, path, 0)
}

// ReadLimitContext is ReadLimit with its retry of a denied open bounded by ctx.
func ReadLimitContext(ctx context.Context, path string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 || maxBytes == math.MaxInt64 {
		return nil, errors.New("cas: invalid read limit")
	}
	return readLimitContext(ctx, path, maxBytes)
}

func readLimit(path string, maxBytes int64) ([]byte, error) {
	return readLimitContext(context.Background(), path, maxBytes)
}

func readLimitContext(ctx context.Context, path string, maxBytes int64) ([]byte, error) {
	b, err := readFileLimit(ctx, path, maxBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if b == nil {
		b = []byte{}
	}
	return b, err
}

func Write(path string, base, data []byte) error {
	pl, err := beside(path)
	if err != nil {
		return err
	}
	return writeAt(pl, base, data)
}

func Change(path string, edit func(cur []byte) ([]byte, error)) error {
	pl, err := beside(path)
	if err != nil {
		return err
	}
	return changeAt(pl, edit, Read)
}

// ChangeLimit bounds initial and compare reads under the existing edit lock,
// and refuses an oversized replacement before staging it.
func ChangeLimit(path string, maxBytes int64, edit func([]byte) ([]byte, error)) error {
	return ChangeLimitContext(context.Background(), path, maxBytes, edit)
}

// ChangeLimitContext is ChangeLimit with lock acquisition and comparison reads
// bounded by ctx. Cancellation before replacement leaves the value unchanged.
// Once replacement starts, the call waits for its synchronous filesystem result.
func ChangeLimitContext(ctx context.Context, path string, maxBytes int64, edit func([]byte) ([]byte, error)) error {
	if maxBytes <= 0 || maxBytes == math.MaxInt64 {
		return errors.New("cas: invalid read limit")
	}
	pl, err := beside(path)
	if err != nil {
		return err
	}
	return changeAtContext(ctx, pl, func(cur []byte) ([]byte, error) {
		next, err := edit(cur)
		if err == nil && int64(len(next)) > maxBytes {
			return nil, ErrTooLarge
		}
		return next, err
	}, func(ctx context.Context, path string) ([]byte, error) { return ReadLimitContext(ctx, path, maxBytes) })
}

// ReplaceLimit replaces a small record whose owner does not need its old
// value. The limit applies to the new value, so an invalid old record can be
// repaired without reading it. The replacement uses the same local path,
// canonical lock and durable write as ChangeLimit.
func ReplaceLimit(path string, maxBytes int64, data []byte) error {
	if maxBytes <= 0 || maxBytes == math.MaxInt64 {
		return errors.New("cas: invalid write limit")
	}
	if data == nil {
		return ErrNoValue
	}
	if int64(len(data)) > maxBytes {
		return ErrTooLarge
	}
	pl, err := beside(path)
	if err != nil {
		return err
	}
	ctx := context.Background()
	unlock, err := lockAtContext(ctx, pl)
	if err != nil {
		return err
	}
	defer unlock()
	return replaceAtContext(ctx, pl, data)
}

// placed names the files one write uses: the data file, its lock file and the
// directory its replacement is staged in.
type placed struct{ target, lock, stage string }

// beside is the default placement: the lock and the staged file sit beside the
// data file. Every spelling of one file names one lock, so writers through
// different spellings exclude each other. The lock is keyed on the parent folder plus the leaf
// (fileid.ResolveLeaf, CAS-L1): the folder is resolved through junctions,
// symbolic links and 8.3 aliases to its final path, and the leaf reads as the
// folder holds it, an 8.3 alias in its long form. The data file's own
// identity is never the key, since every write renames a new file over it.
// A folder that does not exist yet is created first, as the lock creates it.
// The lock holds within one host, so a path off this host's local volumes, or
// a folder reached through a link to one, is refused (LocalPath).
func beside(path string) (placed, error) {
	if err := LocalPath(path); err != nil {
		return placed{}, err
	}
	leaf, err := leafOf(path, true)
	if err != nil {
		return placed{}, err
	}
	target := leaf.Path()
	return placed{target: target, lock: target + ".lock", stage: leaf.Folder().Path()}, nil
}

// leafOf is fileid.ResolveLeaf of path in this process's view, and a refusal
// named as the lock's. With create, a missing folder is created first, as
// the lock creates it; without, a missing folder answers fs.ErrNotExist.
func leafOf(path string, create bool) (fileid.Leaf, error) {
	leaf, err := fileid.ResolveLeaf(context.Background(), fileid.Self, path)
	if create && errors.Is(err, fs.ErrNotExist) {
		pending, pendingErr := fileid.Pending(context.Background(), fileid.Self, path)
		if pendingErr != nil {
			return fileid.Leaf{}, pendingErr
		}
		if err := os.MkdirAll(filepath.Dir(pending.Path()), 0o700); err != nil {
			return fileid.Leaf{}, err
		}
		leaf, err = fileid.ResolveLeaf(context.Background(), fileid.Self, path)
	}
	if errors.Is(err, identity.ErrNotLocalPath) {
		return fileid.Leaf{}, fmt.Errorf("cas: the lock holds within one host: %w", err)
	}
	return leaf, err
}

// LocalPath reports whether Write, Change and Sweep, and a placement's root
// and side, accept path (CAS-L2). The lock holds within one host, so a path
// off this host's local volumes is refused with identity's reason; the error
// matches errors.Is(err, identity.ErrNotLocalPath) and names the path once.
// A store that writes through cas calls it where its root is configured, and
// the refusal comes there, before the first write. A relative path is read
// against the working folder, as Write reads it.
func LocalPath(path string) error {
	if err := identity.LocalPath(path); err != nil {
		return fmt.Errorf("cas: the lock holds within one host: %w", err)
	}
	return nil
}

func writeAt(pl placed, base, data []byte) error {
	unlock, err := lockAt(pl)
	if err != nil {
		return err
	}
	defer unlock()
	return writeReadAt(pl, base, data, Read)
}

func changeAt(pl placed, edit func([]byte) ([]byte, error), read func(string) ([]byte, error)) error {
	unlock, err := lockAt(pl)
	if err != nil {
		return err
	}
	defer unlock()
	cur, err := read(pl.target)
	if err != nil {
		return err
	}
	next, err := edit(cur)
	if err != nil {
		return err
	}
	if same(cur, next) {
		return nil
	}
	return writeReadAt(pl, cur, next, read)
}

func changeAtContext(ctx context.Context, pl placed, edit func([]byte) ([]byte, error), read func(context.Context, string) ([]byte, error)) error {
	unlock, err := lockAtContext(ctx, pl)
	if err != nil {
		return err
	}
	defer unlock()
	cur, err := read(ctx, pl.target)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	next, err := edit(cur)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if same(cur, next) {
		return nil
	}
	return writeReadAtContext(ctx, pl, cur, next, read)
}

func same(a, b []byte) bool { return (a == nil) == (b == nil) && bytes.Equal(a, b) }

func lock(path string) (func(), error) {
	pl, err := beside(path)
	if err != nil {
		return nil, err
	}
	return lockAt(pl)
}

func lockAt(pl placed) (func(), error) {
	for _, dir := range []string{filepath.Dir(pl.target), filepath.Dir(pl.lock)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(pl.lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := flock(f); err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil
}

// LockContext holds the same native process lock used by CAS updates. Shared
// holders may coexist; an exclusive holder excludes every shared holder. The
// caller owns the path boundary and releases the lock after its whole operation.
func LockContext(ctx context.Context, path string, shared bool) (func(), error) {
	pl, err := beside(path)
	if err != nil {
		return nil, err
	}
	bounded, cancel := context.WithCancel(ctx)
	defer cancel()
	return lockAtContextMode(bounded, pl, shared)
}

func lockAtContext(ctx context.Context, pl placed) (func(), error) {
	return lockAtContextMode(ctx, pl, false)
}

func lockAtContextMode(ctx context.Context, pl placed, shared bool) (func(), error) {
	if ctx.Done() == nil && !shared {
		return lockAt(pl)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, dir := range []string{filepath.Dir(pl.target), filepath.Dir(pl.lock)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(pl.lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		acquired, err := tryFlockMode(f, shared)
		if err != nil {
			//unchecked: best-effort cleanup on a path that already returns a definite error
			f.Close()
			return nil, err
		}
		if acquired {
			if err := ctx.Err(); err != nil {
				f.Close()
				return nil, err
			}
			//unchecked: the returned unlock closure has no return value to report a close failure through
			return func() { f.Close() }, nil
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			//unchecked: best-effort cleanup on a path that already returns a definite error
			f.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// afterStage runs between staging and the rename. Tests set it to kill a writer
// at that point.
var afterStage func(tmp string)

// syncParent flushes the directory whose entry the rename replaced. It is
// SyncDir; tests replace it to observe the call.
var syncParent = SyncDir

// Sweep removes the staged files a killed writer left beside path and returns
// how many. It holds the lock, and a live writer stages under the lock, so
// every staged file it finds is a dead writer's.
func Sweep(path string) (int, error) {
	pl, err := beside(path)
	if err != nil {
		return 0, err
	}
	return sweepAt(pl)
}

func sweepAt(pl placed) (int, error) {
	unlock, err := lockAt(pl)
	if err != nil {
		return 0, err
	}
	defer unlock()
	entries, err := os.ReadDir(pl.stage)
	if err != nil {
		return 0, err
	}
	prefix := filepath.Base(pl.target) + "."
	gone := 0
	for _, e := range entries {
		if e.IsDir() || !stagedName(e.Name(), prefix) {
			continue
		}
		if os.Remove(filepath.Join(pl.stage, e.Name())) == nil {
			gone++
		}
	}
	return gone, nil
}

// stagedName reports whether name is <prefix><unique>.tmp, where the unique
// part is letters, digits and underscores: the names CreateTemp, Python's
// mkstemp and the C++ writer give a replacement. A dot in the unique part
// belongs to another file's name, such as <path>.lock.<unique>.tmp.
func stagedName(name, prefix string) bool {
	const suffix = ".tmp"
	if len(name) <= len(prefix)+len(suffix) || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return false
	}
	for _, r := range name[len(prefix) : len(name)-len(suffix)] {
		if !(r == '_' || '0' <= r && r <= '9' || 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z') {
			return false
		}
	}
	return true
}

func writeReadAt(pl placed, base, data []byte, read func(string) ([]byte, error)) error {
	if data == nil {
		return ErrNoValue
	}
	cur, err := read(pl.target)
	if err != nil {
		return err
	}
	if !same(cur, base) {
		return ErrMoved
	}
	tmp, err := stageIn(pl.stage, filepath.Base(pl.target), data)
	if err != nil {
		return err
	}
	if afterStage != nil {
		afterStage(tmp)
	}
	if err := retry(func() error { return rename(tmp, pl.target) }); err != nil {
		os.Remove(tmp)
		return err
	}
	return syncParent(filepath.Dir(pl.target))
}

func writeReadAtContext(ctx context.Context, pl placed, base, data []byte, read func(context.Context, string) ([]byte, error)) error {
	if data == nil {
		return ErrNoValue
	}
	cur, err := read(ctx, pl.target)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !same(cur, base) {
		return ErrMoved
	}
	return replaceAtContext(ctx, pl, data)
}

func replaceAtContext(ctx context.Context, pl placed, data []byte) error {
	tmp, err := stageIn(pl.stage, filepath.Base(pl.target), data)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		//unchecked: best-effort staged-temp-file cleanup on a path that already returns a definite error
		os.Remove(tmp)
		return err
	}
	if afterStage != nil {
		afterStage(tmp)
	}
	if err := ctx.Err(); err != nil {
		//unchecked: best-effort staged-temp-file cleanup on a path that already returns a definite error
		os.Remove(tmp)
		return err
	}
	if err := retry(func() error { return rename(tmp, pl.target) }); err != nil {
		//unchecked: best-effort staged-temp-file cleanup on a path that already returns a definite error
		os.Remove(tmp)
		return err
	}
	return syncParent(filepath.Dir(pl.target))
}

func readFile(path string) ([]byte, error) { return readFileLimit(context.Background(), path, 0) }

// readFileLimit reads path, bounded by maxBytes when positive, and retries a
// denied open until its try count or ctx ends.
func readFileLimit(ctx context.Context, path string, maxBytes int64) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var f *os.File
	// A read races every writer's rename. While a replaced file is being
	// deleted, opening its name answers access denied or a sharing violation;
	// the rename already retries those, and the read does too, until its try
	// count or the caller's context ends.
	for tries := 0; ; tries++ {
		if maxBytes > 0 {
			f, err = openBoundedRecord(root, filepath.Base(path))
		} else {
			f, err = root.Open(filepath.Base(path))
		}
		if err == nil || !transient(err) || tries >= 2000 {
			break
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: %w: %w", ErrRefused, ctx.Err(), err)
		}
		if tries >= 50 {
			time.Sleep(time.Millisecond)
		}
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if maxBytes == 0 {
		return io.ReadAll(f)
	}
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("cas: bounded record must be regular")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err == nil && int64(len(data)) > maxBytes {
		return nil, ErrTooLarge
	}
	return data, err
}

// rename replaces path with tmp through an os.Root at the deepest directory
// holding both, which keeps the replace-with-POSIX-semantics rename when a
// placement stages in another directory of the same volume.
func rename(tmp, path string) error { return renameFile(tmp, path) }

func renameRoot(tmp, path string) error {
	dir := filepath.Dir(path)
	if filepath.Dir(tmp) != dir {
		dir = commonDir(filepath.Dir(tmp), dir)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	from, inside, err := physicalRelative(tmp, dir)
	if err != nil || !inside {
		if err == nil {
			err = ErrOutsideRoot
		}
		return err
	}
	to, inside, err := physicalRelative(path, dir)
	if err != nil || !inside {
		if err == nil {
			err = ErrOutsideRoot
		}
		return err
	}
	return root.Rename(from, to)
}

func stage(path string, data []byte) (string, error) {
	return stageIn(filepath.Dir(path), filepath.Base(path), data)
}

func stageIn(dir, name string, data []byte) (string, error) {
	f, err := os.CreateTemp(dir, name+".*.tmp")
	if err != nil {
		return "", err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func retry(op func() error) error {
	var err error
	for range 1000 {
		if err = op(); !transient(err) {
			return err
		}
	}
	return err
}
