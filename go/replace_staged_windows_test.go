package cas

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	identity "github.com/openabstractions/abstraction-identity"
	"github.com/openabstractions/abstraction-identity/fileid"
)

func replacementStage(t *testing.T, target string, data []byte) (string, fileid.FileID, [32]byte) {
	t.Helper()
	pl, err := beside(target)
	if err != nil {
		t.Fatal(err)
	}
	name, err := stageIn(pl.stage, filepath.Base(pl.target), data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(name) })
	id, err := fileid.Resolve(t.Context(), fileid.Self, name, fileid.Options{Links: fileid.RefuseLinks})
	if err != nil {
		t.Fatal(err)
	}
	return name, id, sha256.Sum256(data)
}

func TestReplaceStagedPreservesHardlinkedSourceAndNeedsItsExactPin(t *testing.T) {
	for _, mode := range []string{"valid", "changed bytes", "changed object", "extra link", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			original := filepath.Join(root, "original")
			target := filepath.Join(root, "python.exe")
			if err := os.WriteFile(original, []byte("shared"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(original, target); err != nil {
				t.Fatal(err)
			}
			stage, id, hash := replacementStage(t, target, []byte("copied"))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "changed bytes":
				if err := os.WriteFile(stage, []byte("edited"), 0600); err != nil {
					t.Fatal(err)
				}
			case "changed object":
				if err := os.Remove(stage); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(stage, []byte("copied"), 0600); err != nil {
					t.Fatal(err)
				}
			case "extra link":
				if err := os.Link(stage, filepath.Join(root, "other")); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				cancel()
			}
			err := ReplaceStagedContext(ctx, target, stage, id, hash, 6)
			if mode == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				data, _ := os.ReadFile(target)
				if string(data) != "copied" {
					t.Fatal(string(data))
				}
				a, err := fileid.Resolve(t.Context(), fileid.Self, target, fileid.Options{Links: fileid.RefuseLinks})
				if err != nil {
					t.Fatal(err)
				}
				b, err := fileid.Resolve(t.Context(), fileid.Self, original, fileid.Options{Links: fileid.RefuseLinks})
				if err != nil {
					t.Fatal(err)
				}
				if a.Equal(b) {
					t.Fatal("target remained shared")
				}
			} else {
				if err == nil {
					t.Fatal("unadmitted replacement accepted")
				}
				data, _ := os.ReadFile(target)
				if string(data) != "shared" {
					t.Fatal("failed replacement changed target")
				}
			}
			data, _ := os.ReadFile(original)
			if string(data) != "shared" {
				t.Fatal("shared source changed")
			}
		})
	}
}

func TestReplaceStagedUsesCanonicalTargetLockAndRefusesShares(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "long-target-name.json")
	if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	stage, id, hash := replacementStage(t, target, []byte("new"))
	unlock, err := LockContext(t.Context(), target, false)
	if err != nil {
		t.Fatal(err)
	}
	alias := shortName(t, target)
	if alias == "" {
		alias = target
	}
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	if err := ReplaceStagedContext(ctx, alias, stage, id, hash, 3); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	unlock()
	if err := ReplaceStagedContext(t.Context(), alias, stage, id, hash, 3); err != nil {
		t.Fatal(err)
	}
	stage, id, hash = replacementStage(t, target, []byte("bad"))
	if err := ReplaceStagedContext(t.Context(), shareSpelling(t, target), stage, id, hash, 3); !errors.Is(err, identity.ErrNotLocalPath) {
		t.Fatalf("share accepted: %v", err)
	}
}

func TestReplaceStagedRefusesParentChangedBeforeLock(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "parent")
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(folder, "python.exe")
	if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	stage, id, hash := replacementStage(t, target, []byte("new"))
	beforeStagedLock = func() {
		moved := folder + ".old"
		if err := os.Rename(folder, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(folder, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("replacement parent"), 0600); err != nil {
			t.Fatal(err)
		}
		// The exact pinned staged object follows into the replacement parent.
		if err := os.Rename(filepath.Join(moved, filepath.Base(stage)), stage); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { beforeStagedLock = nil }()
	if err := ReplaceStagedContext(t.Context(), target, stage, id, hash, 3); !errors.Is(err, ErrMoved) {
		t.Fatalf("parent redirect accepted: %v", err)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "replacement parent" {
		t.Fatal("replacement parent written")
	}
}

func TestReplaceStagedChecksCancellationAfterHandleAdmission(t *testing.T) {
	target := filepath.Join(t.TempDir(), "python.exe")
	if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	stage, _, _ := replacementStage(t, target, []byte("new"))
	leaf, err := leafOf(target, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err = renameFileChecked(stage, target, leaf.Folder(), ctx, func(*os.File) error { cancel(); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "old" {
		t.Fatal("cancelled admitted stage was renamed")
	}
}
