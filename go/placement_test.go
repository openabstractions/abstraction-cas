package cas

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// tree lists every file under dir as slash-separated paths relative to it.
func tree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func newTestPlacement(t *testing.T) (*Placement, string) {
	t.Helper()
	base := t.TempDir()
	p, err := NewPlacement(filepath.Join(base, "root"), filepath.Join(base, "side"))
	if err != nil {
		t.Fatal(err)
	}
	return p, base
}

func placedEnv(p *Placement, path, role string, n int) []string {
	return append(os.Environ(), "CAS_PATH="+path, "CAS_ROOT="+p.Root(), "CAS_SIDE="+p.Side(), "CAS_ROLE="+role, "CAS_N="+strconv.Itoa(n))
}

func TestPlacementKeepsLocksAndStagingOutOfRoot(t *testing.T) {
	p, _ := newTestPlacement(t)
	path := filepath.Join(p.Root(), "host", "ns", "model", "latest")
	if err := p.Write(path, nil, []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := p.Write(path, nil, []byte("2")); !errors.Is(err, ErrMoved) {
		t.Fatalf("nil base on an existing file: %v", err)
	}
	if err := p.Change(path, func(cur []byte) ([]byte, error) { return append(cur, '!'), nil }); err != nil {
		t.Fatal(err)
	}
	if b, err := p.Read(path); err != nil || string(b) != "1!" {
		t.Fatalf("read %q, %v", b, err)
	}
	if got := tree(t, p.Root()); strings.Join(got, ",") != "host/ns/model/latest" {
		t.Fatalf("root holds %v", got)
	}
	if got := tree(t, p.Side()); strings.Join(got, ",") != "host/ns/model/latest.lock" {
		t.Fatalf("side holds %v", got)
	}
}

func TestPlacementSideInsideRoot(t *testing.T) {
	root := t.TempDir()
	p, err := NewPlacement(root, filepath.Join(root, ".cas"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "a", "b")
	if err := p.Write(path, nil, []byte("v")); err != nil {
		t.Fatal(err)
	}
	if got := tree(t, root); strings.Join(got, ",") != ".cas/a/b.lock,a/b" {
		t.Fatalf("root holds %v", got)
	}
	if err := p.Write(filepath.Join(root, ".cas", "x"), nil, []byte("x")); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("a file inside the side directory: %v", err)
	}
}

func TestPlacementRefusesWhatItDoesNotCover(t *testing.T) {
	p, base := newTestPlacement(t)
	for _, path := range []string{p.Root(), filepath.Join(base, "elsewhere"), filepath.Join(p.Side(), "x"), filepath.Join(p.Root(), "..", "escape")} {
		if err := p.Write(path, nil, []byte("x")); !errors.Is(err, ErrOutsideRoot) {
			t.Errorf("write %s: %v", path, err)
		}
		if _, err := p.Read(path); !errors.Is(err, ErrOutsideRoot) {
			t.Errorf("read %s: %v", path, err)
		}
	}
	for _, side := range []string{p.Root(), base} {
		if _, err := NewPlacement(p.Root(), side); err == nil {
			t.Errorf("a side directory %s containing the root was accepted", side)
		}
	}
}

func TestPlacementRefusesAnotherVolume(t *testing.T) {
	previous := volumeID
	t.Cleanup(func() { volumeID = previous })
	base := t.TempDir()
	root, side := filepath.Join(base, "root"), filepath.Join(base, "side")
	volumeID = func(dir string) (uint64, error) {
		if filepath.Base(dir) == "side" {
			return 2, nil
		}
		return 1, nil
	}
	if _, err := NewPlacement(root, side); !errors.Is(err, ErrCrossVolume) {
		t.Fatalf("different volume ids: %v", err)
	}
}

func TestPlacementRefusesARealSecondVolume(t *testing.T) {
	base := t.TempDir()
	here, err := volumeOf(base)
	if err != nil {
		t.Fatal(err)
	}
	candidates := []string{"/dev/shm", "/run"}
	if runtime.GOOS == "windows" {
		candidates = nil
		for c := 'A'; c <= 'Z'; c++ {
			candidates = append(candidates, string(c)+`:\`)
		}
	}
	for _, other := range candidates {
		if v, err := volumeOf(other); err != nil || v == here {
			continue
		}
		if _, err := NewPlacement(filepath.Join(base, "root"), other); !errors.Is(err, ErrCrossVolume) {
			t.Fatalf("side %s on another volume than %s: %v", other, base, err)
		}
		return
	}
	t.Skip("no second volume on this machine")
}

func TestPlacementNoLostUpdateAcrossProcesses(t *testing.T) {
	p, _ := newTestPlacement(t)
	path := filepath.Join(p.Root(), "sub", "n")
	const writers, each = 4, 100
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	spawn := func(role string, n int) *exec.Cmd {
		c := exec.Command(exe, "-test.run=^$")
		c.Env = placedEnv(p, path, role, n)
		c.Stderr = os.Stderr
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		return c
	}
	total := writers*each + each
	reader := spawn("reader", total)
	var ws []*exec.Cmd
	for range writers {
		ws = append(ws, spawn("writer", each))
	}
	// A second Placement value in this process takes the same lock.
	q, err := NewPlacement(p.Root(), p.Side())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for range each {
			if err := q.Change(path, increment); err != nil {
				t.Error(err)
				return
			}
		}
	})
	wg.Wait()
	for _, w := range ws {
		if err := w.Wait(); err != nil {
			reader.Process.Kill()
			t.Fatal(err)
		}
	}
	if err := reader.Wait(); err != nil {
		t.Fatal(err)
	}
	b, _ := p.Read(path)
	if got := counter(b); got != total {
		t.Fatalf("%d of %d updates survived", got, total)
	}
	if got := tree(t, p.Root()); strings.Join(got, ",") != "sub/n" {
		t.Fatalf("contention left %v in root", got)
	}
	if got := tree(t, p.Side()); strings.Join(got, ",") != "sub/n.lock" {
		t.Fatalf("contention left %v in side", got)
	}
}

func TestPlacementKilledWriterLeavesTheValueAndTheRootClean(t *testing.T) {
	p, _ := newTestPlacement(t)
	path := filepath.Join(p.Root(), "m", "latest")
	if err := p.Write(path, nil, []byte("1 1")); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := exec.Command(exe, "-test.run=^$")
	c.Env = placedEnv(p, path, "killed", 1)
	c.Stderr = os.Stderr
	out, err := c.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	staged := make(chan bool, 1)
	go func() {
		s := bufio.NewScanner(out)
		for s.Scan() {
			if strings.TrimSpace(s.Text()) == "STAGED" {
				staged <- true
				io.Copy(io.Discard, out)
				return
			}
		}
		staged <- false
	}()
	select {
	case ok := <-staged:
		if !ok {
			c.Wait()
			t.Fatal("the writer exited before staging")
		}
	case <-time.After(30 * time.Second):
		c.Process.Kill()
		t.Fatal("the writer never staged")
	}
	if err := c.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	c.Wait()

	if b, err := p.Read(path); err != nil || string(b) != "1 1" {
		t.Fatalf("after the kill the file holds %q, %v", b, err)
	}
	if got := tree(t, p.Root()); strings.Join(got, ",") != "m/latest" {
		t.Fatalf("the killed writer left %v in root", got)
	}
	side := tree(t, p.Side())
	var tmps int
	for _, f := range side {
		if strings.HasPrefix(f, "m/latest.") && strings.HasSuffix(f, ".tmp") {
			tmps++
		}
	}
	if len(side) != 2 || tmps != 1 {
		t.Fatalf("side holds %v, want the lock and one staged file", side)
	}
	done := make(chan error, 1)
	go func() { done <- p.Change(path, increment) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the killed writer's lock was not released")
	}
	if b, _ := p.Read(path); string(b) != "2 2" {
		t.Fatalf("after the kill a change wrote %q", b)
	}
}
