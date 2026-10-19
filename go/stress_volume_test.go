package cas

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/openabstractions/abstraction-identity/fileid"
)

func stressVolumeContext(t *testing.T) context.Context {
	t.Helper()
	ctx := t.Context()
	if deadline, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline)
		t.Cleanup(cancel)
	}
	return ctx
}

// retryStressVolume belongs only to the pure increments in this stress fixture.
// CAS-L2/ID-F3 permit a local volume probe to refuse after 500 ms. That exact
// refusal occurs before Change publishes: beside/leaf resolution and renameRoot's
// physicalRelative checks precede root.Rename; SyncDir adds no locality refusal.
// Each retry asks Change to prove locality again. Other refusals keep their error.
func retryStressVolume(ctx context.Context, change func() error) error {
	var last error
	for {
		if err := ctx.Err(); err != nil {
			return errors.Join(err, last)
		}
		err := change()
		var locality *fileid.NotLocalPathError
		if !errors.As(err, &locality) || locality.Reason != "its volume did not answer in time" {
			return err
		}
		last = err
		runtime.Gosched()
	}
}

func TestStressVolumeRetryRechecksBeforeRealChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "n")
	attempts, edits := 0, 0
	err := retryStressVolume(stressVolumeContext(t), func() error {
		attempts++
		if attempts == 1 {
			return fmt.Errorf("fixture prepublication refusal: %w", &fileid.NotLocalPathError{
				Path: path, Reason: "its volume did not answer in time"})
		}
		return Change(path, func(cur []byte) ([]byte, error) {
			edits++
			return increment(cur)
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Read(path)
	if err != nil || counter(b) != 1 || attempts < 2 || edits < 1 {
		t.Fatalf("attempts=%d edits=%d value=%q error=%v", attempts, edits, b, err)
	}
}

func TestStressVolumeRetryKeepsOtherRefusals(t *testing.T) {
	for name, refused := range map[string]error{
		"network volume":    &fileid.NotLocalPathError{Path: "fixture", Reason: "a network volume"},
		"different reason":  &fileid.NotLocalPathError{Path: "fixture", Reason: "its volume did not answer in time: permission"},
		"untyped lookalike": errors.New("its volume did not answer in time"),
		"permission":        fs.ErrPermission,
	} {
		t.Run(name, func(t *testing.T) {
			attempts := 0
			err := retryStressVolume(stressVolumeContext(t), func() error { attempts++; return refused })
			if err != refused || attempts != 1 {
				t.Fatalf("attempts=%d error=%v, want original refusal %v", attempts, err, refused)
			}
		})
	}
}

func TestStressVolumeRetryHonoursCaller(t *testing.T) {
	t.Run("expired before effects", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
		defer cancel()
		attempts := 0
		err := retryStressVolume(ctx, func() error { attempts++; return nil })
		if !errors.Is(err, context.DeadlineExceeded) || attempts != 0 {
			t.Fatalf("attempts=%d error=%v", attempts, err)
		}
	})
	t.Run("cancelled after refusal", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		refused := &fileid.NotLocalPathError{Path: "fixture", Reason: "its volume did not answer in time"}
		attempts := 0
		err := retryStressVolume(ctx, func() error { attempts++; cancel(); return refused })
		if !errors.Is(err, context.Canceled) || !errors.Is(err, refused) || attempts != 1 {
			t.Fatalf("attempts=%d error=%v", attempts, err)
		}
	})
}

func TestStressVolumeRetryKeepsActualNetworkRefusal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(shareSpelling(t, dir), "n")
	attempts, edits := 0, 0
	err := retryStressVolume(stressVolumeContext(t), func() error {
		attempts++
		return Change(path, func(cur []byte) ([]byte, error) { edits++; return increment(cur) })
	})
	var locality *fileid.NotLocalPathError
	if !errors.As(err, &locality) || attempts != 1 || edits != 0 {
		t.Fatalf("attempts=%d edits=%d error=%v", attempts, edits, err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("refusal left files %v: %v", entries, err)
	}
}
