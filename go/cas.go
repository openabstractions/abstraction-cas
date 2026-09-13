package cas

import (
	"bytes"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
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

func Write(path string, base, data []byte) error {
	unlock, err := lock(path)
	if err != nil {
		return err
	}
	defer unlock()
	return write(path, base, data)
}

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
	unlock, err := lock(path)
	if err != nil {
		return err
	}
	defer unlock()
	cur, err := read(path)
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
	return writeRead(path, cur, next, read)
}

func same(a, b []byte) bool { return (a == nil) == (b == nil) && bytes.Equal(a, b) }

func lock(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := flock(f); err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil
}

func write(path string, base, data []byte) error { return writeRead(path, base, data, Read) }
func writeRead(path string, base, data []byte, read func(string) ([]byte, error)) error {
	if data == nil {
		return ErrNoValue
	}
	cur, err := read(path)
	if err != nil {
		return err
	}
	if !same(cur, base) {
		return ErrMoved
	}
	tmp, err := stage(path, data)
	if err != nil {
		return err
	}
	err = retry(func() error { return rename(tmp, path) })
	if err != nil {
		os.Remove(tmp)
	}
	return err
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

func rename(tmp, path string) error {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer root.Close()
	return root.Rename(filepath.Base(tmp), filepath.Base(path))
}

func stage(path string, data []byte) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
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
