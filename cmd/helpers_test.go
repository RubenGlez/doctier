package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// git runs a git command in dir, failing the test on error.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	gitEnv(t, dir, nil, args...)
}

// gitEnv runs git in dir with extra environment (e.g. commit dates).
func gitEnv(t *testing.T, dir string, extraEnv []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// initRepo creates a temp git repo, writes the manifest, chdirs into it, and
// returns the repo root.
func initRepo(t *testing.T, manifest string) string {
	t.Helper()
	root := t.TempDir()
	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "config", "user.email", "t@t.t")
	git(t, root, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(root, ".doctier.yml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	return root
}

// write creates a file (and parent dirs) under root.
func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
