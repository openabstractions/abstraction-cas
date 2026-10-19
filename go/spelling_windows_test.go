package cas

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/openabstractions/abstraction-identity/placetest"
)

// shareSpelling reaches the local folder dir through the administrative share
// \\localhost\X$, the spelling a file on a network share has.
func shareSpelling(t *testing.T, dir string) string {
	t.Helper()
	return `\\localhost\` + dir[:1] + `$` + dir[2:]
}

// shortName is the 8.3 alias of an existing path, or "" when the volume made
// none.
func shortName(t *testing.T, long string) string {
	t.Helper()
	p, err := syscall.UTF16PtrFromString(long)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, syscall.MAX_LONG_PATH)
	n, err := syscall.GetShortPathName(p, &buf[0], uint32(len(buf)))
	if err != nil {
		t.Fatalf("GetShortPathName %s: %v", long, err)
	}
	short := syscall.UTF16ToString(buf[:n])
	if filepath.Base(short) == filepath.Base(long) {
		return ""
	}
	return short
}

func spellings(t *testing.T) []spelling {
	t.Helper()
	var out []spelling
	out = append(out, dotDotSpelling(t, t.TempDir()))

	// An 8.3 alias of the folder holding the file.
	root := t.TempDir()
	folder := filepath.Join(root, "long-folder-name")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	if short := shortName(t, folder); short != "" {
		out = append(out, spelling{name: "short-folder", a: filepath.Join(folder, "counter-record"), b: filepath.Join(short, "counter-record")})
	} else {
		out = append(out, spelling{name: "short-folder", skip: folder + " has no 8.3 alias: the volume makes none"})
	}

	// An 8.3 alias of the file itself, which exists before either writer
	// starts.
	file := filepath.Join(t.TempDir(), "counter-record.json")
	if err := os.WriteFile(file, []byte("0 0"), 0o600); err != nil {
		t.Fatal(err)
	}
	if short := shortName(t, file); short != "" {
		out = append(out, spelling{name: "short-file", a: file, b: short})
	} else {
		out = append(out, spelling{name: "short-file", skip: file + " has no 8.3 alias: the volume makes none"})
	}

	// A junction to the folder holding the file.
	root = t.TempDir()
	target, link := filepath.Join(root, "target"), filepath.Join(root, "junction")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if out2, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v: %s", err, out2)
	}
	out = append(out, spelling{name: "junction", a: filepath.Join(target, "counter-record"), b: filepath.Join(link, "counter-record")})
	out = append(out, volumeMountSpelling(t))
	return out
}

func TestPhysicalPathVerbatimAndJunction(t *testing.T) {
	root := t.TempDir()
	physical := `\\?\` + root + `\x.`
	if err := os.Mkdir(physical, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(physical) })
	if err := os.Mkdir(filepath.Join(root, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"verbatim", "junction"} {
		t.Run(mode, func(t *testing.T) {
			path := physical
			if mode == "junction" {
				path = filepath.Join(root, "junction")
				if err := placetest.MountPoint(path, `\??\`+root+`\x.`, root+`\x.`); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Remove(path) })
			}
			physicalWrite(t, physical, path)
		})
	}
}

func TestPhysicalPathNestedPlacement(t *testing.T) {
	root, side := t.TempDir(), t.TempDir()
	physical := `\\?\` + root + `\x.`
	if err := os.Mkdir(physical, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(physical) })
	if err := os.Mkdir(filepath.Join(root, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := NewPlacement(root, side)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Write(physical+`\value`, nil, []byte("value")); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(physical + `\value`); err != nil || string(got) != "value" {
		t.Fatalf("physical data %q: %v", got, err)
	}
	if entries, err := os.ReadDir(filepath.Join(root, "x")); err != nil || len(entries) != 0 {
		t.Fatalf("decoy modified: %v, %v", entries, err)
	}
	if err := p.Write(physical+`\new\deep\value`, nil, []byte("nested")); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(physical + `\new\deep\value`); err != nil || string(got) != "nested" {
		t.Fatalf("pending physical data %q: %v", got, err)
	}
	entries, err := os.ReadDir(side)
	if err != nil || len(entries) != 1 || entries[0].Name() != "x." {
		t.Fatalf("side mirror: %v, %v", entries, err)
	}
	t.Cleanup(func() { os.RemoveAll(`\\?\` + side + `\x.`) })
}

// volumeMountSpelling reaches a folder through a mount point whose target is
// its volume named by GUID, `\??\Volume{GUID}\...`, the reparse data a volume
// mounted in a folder carries (a Dev Drive or a VHD without a letter). It
// names the temporary folder's own volume, which needs no administrator
// rights; mounting a second volume does (cases.json win-no-letter-volume).
func volumeMountSpelling(t *testing.T) spelling {
	t.Helper()
	root := t.TempDir()
	target, link := filepath.Join(root, "target"), filepath.Join(root, "volume mount")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := placetest.VolumeMountPoint(link, target); err != nil {
		return spelling{name: "volume-guid-mount", skip: err.Error()}
	}
	t.Cleanup(func() { os.Remove(link) })
	return spelling{name: "volume-guid-mount", a: filepath.Join(target, "counter-record"), b: filepath.Join(link, "counter-record")}
}
