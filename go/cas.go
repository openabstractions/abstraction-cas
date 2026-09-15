package cas

import (
	"bytes"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
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
func readLimit(path string, maxBytes int64) ([]byte, error) {
	b, err := readFileLimit(path, maxBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if b == nil {
		b = []byte{}
	}
	return b, err
}

func Write(path string, base, data []byte) error { return writeAt(beside(path), base, data) }

func Change(path string, edit func(cur []byte) ([]byte, error)) error {
	return change(path, edit, Read)
}

// ChangeLimit bounds initial and compare reads under the existing edit lock,
// and refuses an oversized replacement before staging it.
func ChangeLimit(path string, maxBytes int64, edit func([]byte) ([]byte, error)) error {
	if maxBytes <= 0 || maxBytes == math.MaxInt64 {
		return errors.New("cas: invalid read limit")
	}
	return change(path, func(cur []byte) ([]byte, error) {
		next, err := edit(cur)
		if err == nil && int64(len(next)) > maxBytes {
			return nil, ErrTooLarge
		}
		return next, err
	}, func(path string) ([]byte, error) { return ReadLimit(path, maxBytes) })
}
func change(path string, edit func([]byte) ([]byte, error), read func(string) ([]byte, error)) error {
	return changeAt(beside(path), edit, read)
}

// placed names the files one write uses: the data file, its lock file and the
// directory its replacement is staged in.
type placed struct{ target, lock, stage string }

// beside is the default placement: the lock and the staged file sit beside the
// data file.
func beside(path string) placed {
	return placed{target: path, lock: path + ".lock", stage: filepath.Dir(path)}
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

func same(a, b []byte) bool { return (a == nil) == (b == nil) && bytes.Equal(a, b) }

func lock(path string) (func(), error) { return lockAt(beside(path)) }

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

func write(path string, base, data []byte) error { return writeReadAt(beside(path), base, data, Read) }
func writeRead(path string, base, data []byte, read func(string) ([]byte, error)) error {
	return writeReadAt(beside(path), base, data, read)
}

// afterStage runs between staging and the rename. Tests set it to kill a writer
// at that point.
var afterStage func(tmp string)

// syncParent flushes the directory whose entry the rename replaced. It is
// syncDir; tests replace it to observe the call.
var syncParent = syncDir

// Sweep removes the staged files a killed writer left beside path and returns
// how many. It holds the lock, and a live writer stages under the lock, so
// every staged file it finds is a dead writer's.
func Sweep(path string) (int, error) { return sweepAt(beside(path)) }

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

func readFile(path string) ([]byte, error) { return readFileLimit(path, 0) }
func readFileLimit(path string, maxBytes int64) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var f *os.File
	if maxBytes > 0 {
		f, err = openBoundedRecord(root, filepath.Base(path))
	} else {
		f, err = root.Open(filepath.Base(path))
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
func rename(tmp, path string) error {
	dir := filepath.Dir(path)
	if filepath.Dir(tmp) != dir {
		dir = commonDir(filepath.Dir(tmp), dir)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	from, err := filepath.Rel(dir, tmp)
	if err != nil {
		return err
	}
	to, err := filepath.Rel(dir, path)
	if err != nil {
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
