package cas

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrCrossVolume refuses a side directory on another volume than the root. A
// staged file there could not be renamed over its target.
var ErrCrossVolume = errors.New("cas: side directory is on another volume than the root")

// ErrOutsideRoot refuses a path a Placement does not cover: the root itself, a
// path outside it, or a path inside the side directory.
var ErrOutsideRoot = errors.New("cas: path is not a file under the placement root")

// Placement keeps the lock file and the staged replacements of every file under
// its root in a side directory on the same volume, so a directory read by
// enumeration holds only its data files. For root/a/b the lock is
// side/a/b.lock, and a replacement is staged as side/a/b.<unique>.tmp before it
// is renamed over root/a/b.
//
// Every writer of one file must use the same placement. A writer beside the
// file and a writer through a Placement take different locks and exclude
// nobody.
type Placement struct{ root, side string }

// volumeID reports the volume a directory is on; tests replace it.
var volumeID = volumeOf

// NewPlacement creates root and side when they are missing. It refuses a side
// directory that is or contains root, and one on another volume.
func NewPlacement(root, side string) (*Placement, error) {
	r, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	s, err := filepath.Abs(side)
	if err != nil {
		return nil, err
	}
	if within(r, s) {
		return nil, fmt.Errorf("cas: side directory %s contains root %s", s, r)
	}
	for _, dir := range []string{r, s} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	rv, err := volumeID(r)
	if err != nil {
		return nil, err
	}
	sv, err := volumeID(s)
	if err != nil {
		return nil, err
	}
	if rv != sv {
		return nil, fmt.Errorf("%w: root %s, side %s", ErrCrossVolume, r, s)
	}
	return &Placement{root: r, side: s}, nil
}

// Root is the absolute directory whose files the placement covers.
func (p *Placement) Root() string { return p.root }

// Side is the absolute directory holding locks and staged replacements.
func (p *Placement) Side() string { return p.side }

// Read is Read for a file the placement covers.
func (p *Placement) Read(path string) ([]byte, error) {
	pl, err := p.place(path)
	if err != nil {
		return nil, err
	}
	return Read(pl.target)
}

// Write is Write with the lock and the staged file in the side directory.
func (p *Placement) Write(path string, base, data []byte) error {
	pl, err := p.place(path)
	if err != nil {
		return err
	}
	return writeAt(pl, base, data)
}

// Change is Change with the lock and the staged file in the side directory.
func (p *Placement) Change(path string, edit func(cur []byte) ([]byte, error)) error {
	pl, err := p.place(path)
	if err != nil {
		return err
	}
	return changeAt(pl, edit, Read)
}

// Sweep is Sweep for the staged files a killed writer left in the side
// directory for path.
func (p *Placement) Sweep(path string) (int, error) {
	pl, err := p.place(path)
	if err != nil {
		return 0, err
	}
	return sweepAt(pl)
}

func (p *Placement) place(path string) (placed, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return placed{}, err
	}
	rel, err := filepath.Rel(p.root, abs)
	if err != nil || rel == "." || !within(abs, p.root) || within(abs, p.side) {
		return placed{}, fmt.Errorf("%w: %s (root %s, side %s)", ErrOutsideRoot, path, p.root, p.side)
	}
	mirror := filepath.Join(p.side, rel)
	return placed{target: abs, lock: mirror + ".lock", stage: filepath.Dir(mirror)}, nil
}

// within reports whether child is parent or lies under it.
func within(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// commonDir is the deepest directory holding both a and b.
func commonDir(a, b string) string {
	for dir := a; ; dir = filepath.Dir(dir) {
		if within(b, dir) || filepath.Dir(dir) == dir {
			return dir
		}
	}
}
