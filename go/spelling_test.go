package cas

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// spelling is one data file named two ways. The file the writers count in is
// a; b reaches the same file through another spelling. A spelling this
// machine cannot make carries skip, and its subtest reports SKIP with it.
type spelling struct{ name, a, b, skip string }

// physicalWrite compares CAS with the operating system on the original
// spelling. The physical folder and a decoy both remain owned by the test.
func physicalWrite(t *testing.T, folder, spelling string) {
	t.Helper()
	for _, mode := range []string{"beside", "placement"} {
		t.Run(mode, func(t *testing.T) {
			path := spelling + string(filepath.Separator) + mode
			physical := folder + string(filepath.Separator) + mode
			if err := os.WriteFile(path, []byte("base"), 0o600); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(physical); err != nil || string(got) != "base" {
				t.Fatalf("kernel oracle: %q, %v", got, err)
			}
			write := Write
			if mode == "placement" {
				p, err := NewPlacement(spelling, t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				write = p.Write
			}
			if err := write(path, []byte("base"), []byte("next")); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(physical); err != nil || string(got) != "next" {
				t.Fatalf("physical target: %q, %v", got, err)
			}
		})
	}
}

func TestPhysicalPathReplacesHardLinkEntry(t *testing.T) {
	root := t.TempDir()
	untouched, target := filepath.Join(root, "original"), filepath.Join(root, "entry")
	if err := os.WriteFile(untouched, []byte("base"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(untouched, target); err != nil {
		t.Fatal(err)
	}
	if err := Write(target, []byte("base"), []byte("next")); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(untouched); err != nil || string(got) != "base" {
		t.Fatalf("linked file changed: %q, %v", got, err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "next" {
		t.Fatalf("replacement: %q, %v", got, err)
	}
}

// dotDotSpelling reaches dir/counter-record through a real folder and "..".
func dotDotSpelling(t *testing.T, dir string) spelling {
	t.Helper()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	return spelling{
		name: "dot-dot",
		a:    filepath.Join(dir, "counter-record"),
		b:    filepath.Join(dir, "sub") + string(filepath.Separator) + ".." + string(filepath.Separator) + "counter-record",
	}
}

// Writers that name one file through two spellings exclude each other: the
// lock is named from the file the spelling reaches, so no update is lost and
// Change never answers ErrMoved. Each spelling keeps the file at its own name.
// The same holds for writers through one Placement.
func TestTwoSpellingsOfOneFileLoseNoUpdate(t *testing.T) {
	for _, s := range spellings(t) {
		t.Run(s.name, func(t *testing.T) {
			if s.skip != "" {
				t.Skip(s.skip)
			}
			countThroughBoth(t, s, func(path string, w int) error {
				if w%4 >= 2 {
					return ChangeLimitContext(context.Background(), path, 64, increment)
				}
				return Change(path, increment)
			})
		})
	}
	for _, s := range spellings(t) {
		t.Run("placement/"+s.name, func(t *testing.T) {
			if s.skip != "" {
				t.Skip(s.skip)
			}
			p, err := NewPlacement(filepath.Dir(s.a), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			countThroughBoth(t, s, func(path string, _ int) error { return p.Change(path, increment) })
		})
	}
}

// countThroughBoth runs eight writers, half through each spelling of s, and
// requires every one of their 320 updates and no ErrMoved. Every update
// renames a new file over the data file, so the file's own identity changes
// between two writers' resolves (W0-06(a), 1b52c14e3: a lock named from that
// identity lost 160 and 171 of 320).
func countThroughBoth(t *testing.T, s spelling, change func(path string, writer int) error) {
	t.Helper()
	const writers, each = 8, 40
	start, err := Read(s.a)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	moved := 0
	for w := range writers {
		path := s.a
		if w%2 == 1 {
			path = s.b
		}
		wg.Go(func() {
			for range each {
				err := change(path, w)
				if errors.Is(err, ErrMoved) {
					mu.Lock()
					moved++
					mu.Unlock()
					continue
				}
				if err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	b, err := Read(s.a)
	if err != nil {
		t.Fatal(err)
	}
	if got := counter(b) - counter(start); got != writers*each || moved != 0 {
		t.Fatalf("%s and %s: %d of %d updates survived, %d answered ErrMoved", s.a, s.b, got, writers*each, moved)
	}
	entries, err := os.ReadDir(filepath.Dir(s.a))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == filepath.Base(s.a) {
			return
		}
	}
	t.Fatalf("%s is gone; the folder holds %v", s.a, entries)
}
