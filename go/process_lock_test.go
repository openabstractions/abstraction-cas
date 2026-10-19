package cas

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestProcessLockSharedHoldersExcludeResetUntilAllRelease(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings")
	first, err := LockContext(context.Background(), p, true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LockContext(context.Background(), p, true)
	if err != nil {
		first()
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	_, err = LockContext(ctx, p, false)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	first()
	second()
	exclusive, err := LockContext(context.Background(), p, false)
	if err != nil {
		t.Fatal(err)
	}
	defer exclusive()
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = LockContext(ctx, p, true)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
