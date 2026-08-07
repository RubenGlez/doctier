//go:build !windows

package cmd

import (
	"os"
	"testing"
)

func assertOwnerOnlyPermissions(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("unlock must write 0600, got %o", perm)
	}
}
