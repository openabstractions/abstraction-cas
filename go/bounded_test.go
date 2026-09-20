package cas

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestChangeLimitContextCancelsHeldNativeLockWithoutLateCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record")
	unlock, err := lock(path)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- ChangeLimitContext(ctx, path, 16, func([]byte) ([]byte, error) {
			called = true
			return []byte("committed"), nil
		})
	}()
	select {
	case err = <-result:
	case <-time.After(time.Second):
		unlock()
		<-result
		t.Fatal("held native lock ignored the context deadline")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		unlock()
		t.Fatalf("held native lock: %v", err)
	}
	if called {
		unlock()
		t.Fatal("edit ran without acquiring the native lock")
	}
	unlock()
	time.Sleep(20 * time.Millisecond)
	if data, readErr := Read(path); readErr != nil || data != nil {
		t.Fatalf("cancelled lock wait committed later: %q %v", data, readErr)
	}
	if err := ChangeLimitContext(context.Background(), path, 16, func([]byte) ([]byte, error) {
		return []byte("committed"), nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestChangeLimitContextCancellationDuringEditDoesNotCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record")
	if err := os.WriteFile(path, []byte("base"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	err := ChangeLimitContext(ctx, path, 16, func([]byte) ([]byte, error) {
		cancel()
		return []byte("changed"), nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled edit: %v", err)
	}
	if data, readErr := os.ReadFile(path); readErr != nil || string(data) != "base" {
		t.Fatalf("cancelled edit wrote %q %v", data, readErr)
	}
}

func TestBoundedProviderChange(t *testing.T) {
	for _, phase := range []string{"initial", "recheck", "replacement"} {
		t.Run(phase, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "record")
			initial := []byte("base")
			if phase == "initial" {
				initial = []byte("too-large")
			}
			if err := os.WriteFile(path, initial, 0600); err != nil {
				t.Fatal(err)
			}
			called := false
			err := ChangeLimit(path, 4, func(cur []byte) ([]byte, error) {
				called = true
				if phase == "recheck" {
					if err := os.WriteFile(path, []byte("too-large"), 0600); err != nil {
						return nil, err
					}
				}
				if phase == "replacement" {
					return []byte("too-large"), nil
				}
				return []byte("next"), nil
			})
			if !errors.Is(err, ErrTooLarge) {
				t.Fatalf("limit: %v", err)
			}
			if phase == "initial" && called {
				t.Fatal("oversize entered callback")
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			expected := "too-large"
			if phase == "replacement" {
				expected = "base"
			}
			if string(after) != expected {
				t.Fatalf("refusal wrote %q", after)
			}
		})
	}
	for _, limit := range []int64{-1, 0, math.MaxInt64} {
		if _, err := ReadLimit("absent", limit); err == nil {
			t.Fatal("invalid limit accepted")
		}
		if err := ChangeLimit("absent", limit, func(b []byte) ([]byte, error) { t.Fatal("callback"); return b, nil }); err == nil {
			t.Fatal("invalid change limit")
		}
	}
	path := filepath.Join(t.TempDir(), "missing")
	if b, err := ReadLimit(path, 4); err != nil || b != nil {
		t.Fatalf("missing %q %v", b, err)
	}
	if err := ChangeLimit(path, 4, func([]byte) ([]byte, error) { return []byte("four"), nil }); err != nil {
		t.Fatal(err)
	}
	if b, err := ReadLimit(path, 4); err != nil || string(b) != "four" {
		t.Fatalf("boundary %q %v", b, err)
	}
}
