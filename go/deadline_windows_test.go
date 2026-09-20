package cas

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A file whose read access is denied by its ACL answers access denied on every
// open. The read retries that answer because a writer's replace briefly gives
// it. A read under a context returns ErrRefused within the context's budget,
// for the plain and the bounded read.
func TestAReadDeniedForGoodReturnsAtTheCallersDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "denied.json")
	if err := Write(path, nil, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("icacls", path, "/deny", "*S-1-1-0:(RD)").CombinedOutput(); err != nil {
		t.Fatalf("icacls deny: %v\n%s", err, out)
	}
	t.Cleanup(func() { exec.Command("icacls", path, "/reset").Run() })
	if f, err := os.Open(path); err == nil {
		f.Close()
		t.Skip("this account reads the file despite the deny; it cannot make a read-denied file")
	} else if !errors.Is(err, accessDenied) {
		t.Fatalf("a read-denied file answered %v, want access denied", err)
	}
	const budget = 200 * time.Millisecond
	for _, read := range []struct {
		name string
		call func(context.Context) ([]byte, error)
	}{
		{"ReadContext", func(ctx context.Context) ([]byte, error) { return ReadContext(ctx, path) }},
		{"ReadLimitContext", func(ctx context.Context) ([]byte, error) { return ReadLimitContext(ctx, path, 1<<20) }},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), budget)
		start := time.Now()
		data, err := read.call(ctx)
		elapsed := time.Since(start)
		cancel()
		if !errors.Is(err, ErrRefused) || !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, os.ErrPermission) || len(data) != 0 {
			t.Fatalf("%s = %q, %v; want ErrRefused wrapping the deadline and the denial", read.name, data, err)
		}
		if elapsed > budget+300*time.Millisecond {
			t.Fatalf("%s returned after %v, past its %v budget", read.name, elapsed, budget)
		}
	}
}

// A read under a context still waits out a transient denial that settles
// inside the budget: the pending delete a writer's replace leaves behind.
func TestAReadUnderAContextWaitsOutAPendingDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending")
	if err := Write(path, nil, []byte("before")); err != nil {
		t.Fatal(err)
	}
	release := holdDeletePending(t, path)
	done := make(chan struct{})
	go func() {
		time.Sleep(30 * time.Millisecond)
		release()
		close(done)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b, err := ReadContext(ctx, path)
	<-done
	if err != nil || b != nil {
		t.Fatalf("ReadContext = %q, %v; want the name gone once the delete settles", b, err)
	}
}
