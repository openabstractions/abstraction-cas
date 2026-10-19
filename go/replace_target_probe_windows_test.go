package cas

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/openabstractions/abstraction-identity/fileid"
	"golang.org/x/sys/windows"
)

// This controlled kernel probe records the old-target custody tradeoff at the
// production POSIX stage-rename boundary. It supplies no compare/replace API.
func TestOldTargetHoldAtStageRenameBoundary(t *testing.T) {
	for _, allowDelete := range []bool{false, true} {
		name := "exclusive-delete"
		if allowDelete {
			name = "shared-delete"
		}
		t.Run(name, func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "target.exe")
			if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
				t.Fatal(err)
			}
			stage, _, _ := replacementStage(t, target, []byte("new"))
			before, err := fileid.Resolve(t.Context(), fileid.Self, target, fileid.Options{Links: fileid.RefuseLinks})
			if err != nil {
				t.Fatal(err)
			}
			wide, err := windows.UTF16PtrFromString(target)
			if err != nil {
				t.Fatal(err)
			}
			share := uint32(windows.FILE_SHARE_READ)
			if allowDelete {
				share |= windows.FILE_SHARE_DELETE
			}
			h, err := windows.CreateFile(wide, windows.GENERIC_READ|windows.DELETE, share, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
			if err != nil {
				t.Fatal(err)
			}
			old := os.NewFile(uintptr(h), target)
			defer old.Close()
			key, err := fileid.FileKeyOf(old)
			if err != nil || key != before.Key() {
				t.Fatal("old target identity", key, err)
			}
			write, writeErr := windows.CreateFile(wide, windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, 0, 0)
			if writeErr == nil {
				windows.CloseHandle(write)
			}
			moved := target + ".foreign"
			foreignErr := renameFile(target, moved)
			if foreignErr == nil {
				if err := renameFile(moved, target); err != nil {
					t.Fatal("restore foreign rename", err)
				}
			}
			called := false
			renameErr := renameFileChecked(stage, target, beforeParent(t, target), context.Background(), func(f *os.File) error {
				called = true
				return nil
			})
			t.Logf("old held throughout: shareDelete=%v foreignWrite=%v foreignRename=%v stageRename=%v stageChecked=%v", allowDelete, writeErr, foreignErr, renameErr, called)
			if writeErr == nil || !called {
				t.Fatal("probe did not exercise write exclusion and checked stage boundary")
			}
			if !allowDelete && (foreignErr == nil || renameErr == nil) {
				t.Fatal("exclusive old hold unexpectedly allowed namespace replacement")
			}
			if allowDelete && (foreignErr != nil || renameErr != nil) {
				t.Fatal("shared-delete control did not demonstrate competing rename authority")
			}
			var raw [3]byte
			if _, err := old.ReadAt(raw[:], 0); err != nil || string(raw[:]) != "old" {
				t.Fatal("old descriptor changed", raw, err)
			}
		})
	}
}

func beforeParent(t *testing.T, path string) fileid.FileID {
	t.Helper()
	id, err := fileid.Resolve(t.Context(), fileid.Self, filepath.Dir(path), fileid.Options{Links: fileid.RefuseLinks})
	if err != nil {
		t.Fatal(err)
	}
	return id
}
