package cas

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openabstractions/abstraction-identity/fileid"
)

func TestPlacementRefusalNamesResolutionFailure(t *testing.T) {
	for _, phase := range []string{"root", "side"} {
		t.Run(phase, func(t *testing.T) {
			p, base := newTestPlacement(t)
			path := filepath.Join(p.Root(), "value")
			if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			missing := p.Side()
			if phase == "root" {
				missing = p.Root()
			}
			moved := filepath.Join(base, "moved-"+phase)
			if err := os.Rename(missing, moved); err != nil {
				t.Fatal(err)
			}
			if phase == "root" {
				path = filepath.Join(moved, "value")
			}
			_, cause := fileid.Open(context.Background(), fileid.Self, missing, fileid.Options{})
			if !errors.Is(cause, fs.ErrNotExist) {
				t.Fatalf("missing folder: %v", cause)
			}
			edits := 0
			err := p.Change(path, func(cur []byte) ([]byte, error) {
				edits++
				return []byte("changed"), nil
			})
			if !errors.Is(err, ErrOutsideRoot) || edits != 0 {
				t.Fatalf("edits=%d refusal=%v", edits, err)
			}
			if errors.Is(err, fs.ErrNotExist) || errors.Unwrap(err) != ErrOutsideRoot {
				t.Fatalf("resolution diagnostic changed refusal classification: %v", err)
			}
			if !strings.Contains(err.Error(), phase+" resolution: "+cause.Error()) {
				t.Fatalf("refusal %q omits %s cause %q", err, phase, cause)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != "original" {
				t.Fatalf("refused change modified data: %q, %v", data, err)
			}
			t.Logf("unchanged refusal classification, diagnostic: %v", err)
		})
	}
}
