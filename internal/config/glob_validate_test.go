package config

import (
	"os"
	"path/filepath"
	"testing"
)

// H1: a malformed glob must fail manifest load rather than silently matching
// nothing (which would downgrade a private rule to plaintext).
func TestLoadRejectsInvalidGlob(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".doctier.yml")
	manifest := `version: 1
docs:
  - path: "docs/[strategy/**"
    visibility: private
    lifetime: durable
`
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load must reject a malformed glob pattern")
	}
}

func TestLoadRejectsEmptyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".doctier.yml")
	manifest := `version: 1
docs:
  - path: ""
    visibility: private
    lifetime: durable
`
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load must reject an empty path")
	}
}
