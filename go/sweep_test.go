package cas

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestTheDirectoryIsSyncedAfterTheRename(t *testing.T) {
	type call struct{ dir, value string }
	var calls []call
	previous := syncParent
	t.Cleanup(func() { syncParent = previous })

	path := filepath.Join(t.TempDir(), "v")
	syncParent = func(dir string) error {
		b, _ := Read(path)
		calls = append(calls, call{dir, string(b)})
		return previous(dir)
	}
	if err := Write(path, nil, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].dir != filepath.Dir(path) || calls[0].value != "first" {
		t.Fatalf("directory syncs %+v, want one of %s after the rename", calls, filepath.Dir(path))
	}

	calls = nil
	p, _ := newTestPlacement(t)
	placedPath := filepath.Join(p.Root(), "a", "b")
	path = placedPath
	if err := p.Write(placedPath, nil, []byte("placed")); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].dir != filepath.Dir(placedPath) || calls[0].value != "placed" {
		t.Fatalf("placement directory syncs %+v, want one of the target's directory", calls)
	}
}

func TestARealDirectorySyncSucceeds(t *testing.T) {
	if err := syncDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

// killAfterStaging runs a writer that stages one change to path and waits,
// then kills it.
func killAfterStaging(t *testing.T, env []string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := exec.Command(exe, "-test.run=^$")
	c.Env = env
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
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

func TestSweepRemovesAKilledWritersStagedFileOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "n")
	if err := Write(path, nil, []byte("1 1")); err != nil {
		t.Fatal(err)
	}
	killAfterStaging(t, append(os.Environ(), "CAS_PATH="+path, "CAS_ROLE=killed", "CAS_N=1"))
	if b, _ := Read(path); string(b) != "1 1" {
		t.Fatalf("after the kill the file holds %q", b)
	}
	// Names that are not a staged replacement of n: another file's, a dotted
	// unique part, and no unique part.
	decoys := []string{"m.123.tmp", "n.lock.123.tmp", "n.x.y.tmp", "n.tmp"}
	for _, d := range decoys {
		if err := os.WriteFile(filepath.Join(dir, d), []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	before := names(t, dir)
	if len(before) != 2+len(decoys)+1 {
		t.Fatalf("the kill left %v, want the file, its lock, one staged file and the decoys", before)
	}
	if gone, err := Sweep(path); err != nil || gone != 1 {
		t.Fatalf("sweep removed %d, %v", gone, err)
	}
	want := append([]string{"n", "n.lock"}, decoys...)
	sort.Strings(want)
	if got := names(t, dir); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("after the sweep %v, want %v", got, want)
	}
	if gone, err := Sweep(path); err != nil || gone != 0 {
		t.Fatalf("second sweep removed %d, %v", gone, err)
	}
	if err := Change(path, increment); err != nil {
		t.Fatal(err)
	}
	if b, _ := Read(path); string(b) != "2 2" {
		t.Fatalf("after the sweep a change wrote %q", b)
	}
}

func TestPlacementSweepRemovesAKilledWritersStagedFileFromSide(t *testing.T) {
	p, _ := newTestPlacement(t)
	path := filepath.Join(p.Root(), "m", "latest")
	if err := p.Write(path, nil, []byte("1 1")); err != nil {
		t.Fatal(err)
	}
	killAfterStaging(t, placedEnv(p, path, "killed", 1))
	if gone, err := p.Sweep(path); err != nil || gone != 1 {
		t.Fatalf("sweep removed %d, %v", gone, err)
	}
	if got := tree(t, p.Side()); strings.Join(got, ",") != "m/latest.lock" {
		t.Fatalf("side holds %v after the sweep", got)
	}
	if got := tree(t, p.Root()); strings.Join(got, ",") != "m/latest" {
		t.Fatalf("root holds %v", got)
	}
	if _, err := p.Sweep(filepath.Join(p.Side(), "x")); err == nil {
		t.Fatal("a sweep of a path inside side was accepted")
	}
}
