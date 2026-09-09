package cas

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

func counter(cur []byte) int {
	if cur == nil {
		return 0
	}
	var a, b int
	if _, err := fmt.Sscanf(string(cur), "%d %d", &a, &b); err != nil || a != b {
		panic("torn read: " + strconv.Quote(string(cur)))
	}
	return a
}

func increment(cur []byte) ([]byte, error) {
	n := counter(cur) + 1
	return []byte(fmt.Sprintf("%d %d", n, n)), nil
}

func TestMain(m *testing.M) {
	path, n := os.Getenv("CAS_PATH"), os.Getenv("CAS_N")
	if path == "" {
		os.Exit(m.Run())
	}
	target, _ := strconv.Atoi(n)
	switch os.Getenv("CAS_ROLE") {
	case "writer":
		for range target {
			if err := Change(path, increment); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
		}
	case "reader":
		for {
			b, err := Read(path)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
			if counter(b) == target {
				break
			}
		}
	}
	os.Exit(0)
}

func TestStaleWriteIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v")
	if err := Write(path, []byte("x"), []byte("1")); !errors.Is(err, ErrMoved) {
		t.Fatalf("base on a missing file: %v", err)
	}
	if err := Write(path, nil, []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, nil, []byte("2")); !errors.Is(err, ErrMoved) {
		t.Fatalf("nil base on an existing file: %v", err)
	}
	if err := Write(path, []byte("0"), []byte("2")); !errors.Is(err, ErrMoved) {
		t.Fatalf("stale base: %v", err)
	}
	if b, _ := Read(path); string(b) != "1" {
		t.Fatalf("a refused write changed the file: %q", b)
	}
	if err := Write(path, []byte("1"), []byte("2")); err != nil {
		t.Fatal(err)
	}
	if b, _ := Read(path); string(b) != "2" {
		t.Fatalf("got %q", b)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 2 {
		t.Fatalf("refused writes left files behind: %d", len(entries))
	}
}

func TestMissingReadsAsNil(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v")
	if b, err := Read(path); b != nil || err != nil {
		t.Fatalf("got %v, %v", b, err)
	}
	if err := Write(path, nil, []byte{}); err != nil {
		t.Fatal(err)
	}
	if b, _ := Read(path); b == nil {
		t.Fatal("an empty file read as missing")
	}
}

func TestAnEditThatReturnsNoValueIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v")
	if err := Write(path, nil, []byte("before")); err != nil {
		t.Fatal(err)
	}
	if err := Change(path, func([]byte) ([]byte, error) { return nil, nil }); !errors.Is(err, ErrNoValue) {
		t.Fatalf("an edit returning no value on an existing file: %v", err)
	}
	if b, _ := Read(path); string(b) != "before" {
		t.Fatalf("an edit that returned no value left %q", b)
	}
	if err := Write(path, []byte("before"), nil); !errors.Is(err, ErrNoValue) {
		t.Fatalf("a write of no value: %v", err)
	}
	if b, _ := Read(path); string(b) != "before" {
		t.Fatalf("a write of no value left %q", b)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 2 {
		t.Fatalf("a refused write left files behind: %d", len(entries))
	}
}

func TestMissingToEmptyIsAChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v")
	if err := Change(path, func([]byte) ([]byte, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if b, _ := Read(path); b != nil {
		t.Fatalf("an edit that left the file missing created %q", b)
	}
	if err := Change(path, func([]byte) ([]byte, error) { return []byte{}, nil }); err != nil {
		t.Fatal(err)
	}
	if b, _ := Read(path); b == nil {
		t.Fatal("an edit from missing to empty wrote nothing")
	}
}

func TestNoLostUpdateInProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "n")
	const writers, each = 8, 50
	var wg sync.WaitGroup
	for range writers {
		wg.Go(func() {
			for range each {
				if err := Change(path, increment); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	b, _ := Read(path)
	if got := counter(b); got != writers*each {
		t.Fatalf("%d of %d updates survived", got, writers*each)
	}
}

func TestNoLostUpdateAcrossProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "n")
	const writers, each = 4, 100
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	spawn := func(role string, n int) *exec.Cmd {
		c := exec.Command(exe, "-test.run=^$")
		c.Env = append(os.Environ(), "CAS_PATH="+path, "CAS_ROLE="+role, "CAS_N="+strconv.Itoa(n))
		c.Stderr = os.Stderr
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		return c
	}
	reader := spawn("reader", writers*each)
	var ws []*exec.Cmd
	for range writers {
		ws = append(ws, spawn("writer", each))
	}
	for _, w := range ws {
		if err := w.Wait(); err != nil {
			reader.Process.Kill()
			t.Fatal(err)
		}
	}
	if err := reader.Wait(); err != nil {
		t.Fatal(err)
	}
	b, _ := Read(path)
	if got := counter(b); got != writers*each {
		t.Fatalf("%d of %d updates survived", got, writers*each)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 2 {
		t.Fatalf("contention left files behind: %d", len(entries))
	}
}

func TestAnEndedRecordStaysEnded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r")
	ended := errors.New("ended")
	step := func(cur []byte) ([]byte, error) {
		if string(cur) == "done" {
			return nil, ended
		}
		return []byte("running"), nil
	}
	var wg sync.WaitGroup
	var refused, applied int
	var mu sync.Mutex
	for range 16 {
		wg.Go(func() {
			err := Change(path, step)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				applied++
			case errors.Is(err, ended):
				refused++
			default:
				t.Error(err)
			}
		})
	}
	wg.Go(func() {
		if err := Change(path, func([]byte) ([]byte, error) { return []byte("done"), nil }); err != nil {
			t.Error(err)
		}
	})
	wg.Wait()
	if b, _ := Read(path); string(b) != "done" {
		t.Fatalf("walked backwards to %q after %d refusals", b, refused)
	}
	if refused+applied != 16 {
		t.Fatalf("%d refused + %d applied", refused, applied)
	}
}
