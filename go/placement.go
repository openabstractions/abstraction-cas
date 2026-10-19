package cas

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/openabstractions/abstraction-identity/fileid"
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
// directory that is or contains root, one on another volume, and a root or
// side off this host's local volumes (LocalPath), where the lock would hold
// for this host only.
func NewPlacement(root, side string) (*Placement, error) {
	for _, path := range []string{root, side} {
		if err := LocalPath(path); err != nil {
			return nil, err
		}
	}
	r, err := placementFolder(root)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	s, err := placementFolder(side)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	rp, sp := r.FileID().Path(), s.FileID().Path()
	relation, err := r.Relate(s)
	if err != nil || relation.Kind != fileid.RelationOutside {
		return nil, fmt.Errorf("cas: side directory %s contains root %s", sp, rp)
	}
	rv, err := volumeID(rp)
	if err != nil {
		return nil, err
	}
	sv, err := volumeID(sp)
	if err != nil {
		return nil, err
	}
	if rv != sv {
		return nil, fmt.Errorf("%w: root %s, side %s", ErrCrossVolume, rp, sp)
	}
	return &Placement{root: rp, side: sp}, nil
}

func placementFolder(path string) (fileid.Handle, error) {
	h, err := fileid.Open(context.Background(), fileid.Self, path, fileid.Options{})
	if errors.Is(err, fs.ErrNotExist) {
		pending, pendingErr := fileid.Pending(context.Background(), fileid.Self, path)
		if pendingErr != nil {
			return fileid.Handle{}, pendingErr
		}
		if err := os.MkdirAll(pending.Path(), 0o700); err != nil {
			return fileid.Handle{}, err
		}
		h, err = fileid.Open(context.Background(), fileid.Self, path, fileid.Options{})
	}
	if err == nil && !h.FileID().IsDir() {
		h.Close()
		return fileid.Handle{}, fmt.Errorf("cas: %s is not a folder", path)
	}
	return h, err
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
	if err := LocalPath(path); err != nil {
		return placed{}, err
	}
	// The leaf's final spelling, as beside keys it (fileid.ResolveLeaf): the
	// folder after links and aliases, and the leaf as the folder holds it. A
	// folder below root that does not exist yet holds no alias; WithinRel
	// places it by its deepest existing folder.
	var target string
	switch leaf, err := leafOf(path, false); {
	case err == nil:
		target = leaf.Path()
	case errors.Is(err, fs.ErrNotExist):
		pending, err := fileid.Pending(context.Background(), fileid.Self, path)
		if err != nil {
			return placed{}, err
		}
		target = pending.Path()
	case !errors.Is(err, fs.ErrNotExist):
		return placed{}, err
	}
	// Containment by file identity: the data file, the
	// mirrored lock and the stage take the path's own place below root,
	// whatever spelling reached it, so every spelling names one lock and the
	// rename runs below root and side.
	rel, inside, err := placementRelative(target, p.root)
	if err != nil || !inside {
		if err != nil {
			return placed{}, fmt.Errorf("%w: %s (root %s, side %s): root resolution: %v", ErrOutsideRoot, path, p.root, p.side, err)
		}
		return placed{}, fmt.Errorf("%w: %s (root %s, side %s)", ErrOutsideRoot, path, p.root, p.side)
	}
	if _, aside, err := placementRelative(target, p.side); err != nil || aside {
		if err != nil {
			return placed{}, fmt.Errorf("%w: %s (root %s, side %s): side resolution: %v", ErrOutsideRoot, path, p.root, p.side, err)
		}
		return placed{}, fmt.Errorf("%w: %s (root %s, side %s)", ErrOutsideRoot, path, p.root, p.side)
	}
	// These are new mirror names, chosen from fileid's physical remainder.
	// Ask fileid to retain their literal Windows spelling, then use only its
	// returned leaf/folder paths for the lock and staging directory.
	mirrorInput := placementInput(p.side + string(filepath.Separator) + rel)
	if err := mirrorFolder(parentSpelling(mirrorInput), p.side); err != nil {
		return placed{}, err
	}
	mirror, err := leafOf(mirrorInput, false)
	if err != nil {
		return placed{}, err
	}
	if _, inside, err := physicalRelative(mirror.Path(), p.side); err != nil || !inside {
		return placed{}, ErrOutsideRoot
	}
	return placed{target: target, lock: mirror.Path() + ".lock", stage: mirror.Folder().Path()}, nil
}

// The mirror consists of verified physical remainder components. Resolve each
// new directory as a leaf so verbatim names retain their spelling; Pending's
// portable new-name policy intentionally refuses such names.
func mirrorFolder(path, root string) error {
	id, err := fileid.Resolve(context.Background(), fileid.Self, path, fileid.Options{})
	if err == nil {
		if !id.IsDir() {
			return fmt.Errorf("cas: %s is not a folder", path)
		}
		anchor, err := fileid.Resolve(context.Background(), fileid.Self, root, fileid.Options{})
		if err != nil {
			return err
		}
		if !id.Equal(anchor) {
			if _, inside, err := physicalRelative(id.Path(), root); err != nil || !inside {
				return ErrOutsideRoot
			}
		}
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	leaf, err := leafOf(path, false)
	if errors.Is(err, fs.ErrNotExist) {
		parent := parentSpelling(path)
		if parent == path {
			return err
		}
		if err := mirrorFolder(parent, root); err != nil {
			return err
		}
		leaf, err = leafOf(path, false)
	}
	if err != nil {
		return err
	}
	if _, inside, err := physicalRelative(leaf.Path(), root); err != nil || !inside {
		return ErrOutsideRoot
	}
	if err := os.Mkdir(leaf.Path(), 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return nil
}

// Partition an input spelling without cleaning it; fileid owns its walk.
func parentSpelling(path string) string {
	i := strings.LastIndexAny(path, `/`+string(filepath.Separator))
	if i < 0 {
		return "."
	}
	if i <= len(filepath.VolumeName(path)) {
		return path[:i+1]
	}
	return path[:i]
}

// Pending anchors the physical path at root when root contains it. Its Path
// retains the exact resolved spelling, including verbatim Windows components.
func placementRelative(path, root string) (string, bool, error) {
	if _, err := leafOf(path, false); err == nil {
		return physicalRelative(path, root)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", false, err
	}
	id, err := fileid.Resolve(context.Background(), fileid.Self, root, fileid.Options{})
	if err != nil {
		return "", false, err
	}
	pending, err := fileid.Pending(context.Background(), fileid.Self, path, root)
	if err != nil {
		return "", false, err
	}
	if !pending.Root().Equal(id) {
		return "", false, nil
	}
	anchor, target := placementInput(id.Path()), placementInput(pending.Path())
	if anchor == target {
		return "", false, nil
	}
	prefix := strings.TrimRight(anchor, `/`+string(filepath.Separator)) + string(filepath.Separator)
	if !strings.HasPrefix(target, prefix) {
		return "", false, ErrOutsideRoot
	}
	return target[len(prefix):], true, nil
}

func physicalRelative(path, root string) (string, bool, error) {
	leaf, err := leafOf(path, false)
	if err != nil {
		return "", false, err
	}
	folder, err := fileid.Open(context.Background(), fileid.Self, leaf.Folder().Path(), fileid.Options{})
	if err != nil {
		return "", false, err
	}
	defer folder.Close()
	parent, err := fileid.Open(context.Background(), fileid.Self, root, fileid.Options{})
	if err != nil {
		return "", false, err
	}
	defer parent.Close()
	relation, err := folder.Relate(parent)
	if err != nil {
		return "", false, err
	}
	switch relation.Kind {
	case fileid.RelationSame:
		return leaf.Name(), true, nil
	case fileid.RelationInside:
		return filepath.Join(relation.Rel, leaf.Name()), true, nil
	default:
		return "", false, nil
	}
}

// commonDir is the deepest directory holding both a and b, by their
// spelling: rename needs a folder both paths are spelled below, and the
// placement builds both from its own root and side.
func commonDir(a, b string) string {
	for dir := a; ; dir = filepath.Dir(dir) {
		prefix := strings.TrimRight(dir, `/`+string(filepath.Separator)) + string(filepath.Separator)
		if b == dir || strings.HasPrefix(b, prefix) || filepath.Dir(dir) == dir {
			return dir
		}
	}
}
