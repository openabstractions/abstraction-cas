//go:build !windows && !darwin && !linux

package cas

import "testing"

// shareSpelling skips: identity.LocalPath reads every path as local here.
func shareSpelling(t *testing.T, dir string) string {
	t.Helper()
	t.Skip("identity.LocalPath reads every path as local on this platform")
	return dir
}
