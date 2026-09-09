package cas

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
)

var ErrMoved = errors.New("cas: the file changed since it was read")

var ErrNoValue = errors.New("cas: no value to write; missing and empty are different values")

func Read(path string) ([]byte, error) {
	b, err := readFile(path)
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
	unlock, err := lock(path)
	if err != nil {
		return err
	}
	defer unlock()
	cur, err := Read(path)
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
	return write(path, cur, next)
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

func write(path string, base, data []byte) error {
	if data == nil {
		return ErrNoValue
	}
	cur, err := Read(path)
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

func readFile(path string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(filepath.Base(path))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
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
