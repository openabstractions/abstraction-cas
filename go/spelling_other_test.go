//go:build !windows

package cas

import (
	"os"
	"path/filepath"
	"testing"
)

func spellings(t *testing.T) []spelling {
	t.Helper()
	var out []spelling
	out = append(out, dotDotSpelling(t, t.TempDir()))

	// A symbolic link to the folder holding the file.
	root := t.TempDir()
	target, link := filepath.Join(root, "target"), filepath.Join(root, "link")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	out = append(out, spelling{name: "symlinked-folder", a: filepath.Join(target, "counter-record"), b: filepath.Join(link, "counter-record")})

	// ".." after a symbolic link: the kernel walks up from the link's target,
	// and Go's path functions remove the link and ".." from the spelling.
	// Both spellings reach the physical parent of the link's target.
	root = t.TempDir()
	elsewhere := filepath.Join(root, "elsewhere", "deep")
	if err := os.MkdirAll(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	out = append(out, spelling{name: "dot-dot-after-symlink", a: filepath.Join(root, "elsewhere", "counter-record"), b: root + "/link/../counter-record"})
	return out
}

func TestPhysicalPathSymlinkDotDot(t *testing.T) {
	root := t.TempDir()
	physical := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.MkdirAll(filepath.Join(physical, "deep"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(physical, "deep"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	physicalWrite(t, physical, root+"/link/..")
	for _, mode := range []string{"beside", "placement"} {
		t.Run("missing/"+mode, func(t *testing.T) {
			write := Write
			if mode == "placement" {
				p, err := NewPlacement(physical, t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				write = p.Write
			}
			if err := write(root+"/link/../new-"+mode+"/nested/value", nil, []byte("value")); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(filepath.Join(physical, "new-"+mode, "nested", "value")); err != nil || string(got) != "value" {
				t.Fatalf("missing physical target %q: %v", got, err)
			}
		})
	}
}
