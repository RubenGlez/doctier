package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGCPRMergeBranchGating(t *testing.T) {
	root := initRepo(t, `version: 1
docs:
  - path: "**/*.prd.md"
    visibility: public
    lifetime: ephemeral
    expire: { on: pr-merge }
`)
	write(t, root, "f.prd.md", "prd\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "init")

	// On a feature branch, pr-merge collection must be a no-op.
	git(t, root, "switch", "-c", "feature", "-q")
	if err := runGC([]string{"--trigger", "pr-merge"}); err != nil {
		t.Fatalf("gc on feature branch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "f.prd.md")); err != nil {
		t.Fatal("prd must survive on a feature branch")
	}

	// On the integration branch, it must be collected.
	git(t, root, "switch", "main", "-q")
	if err := runGC([]string{"--trigger", "pr-merge"}); err != nil {
		t.Fatalf("gc on main: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "f.prd.md")); !os.IsNotExist(err) {
		t.Fatalf("prd must be collected on the integration branch, stat err=%v", err)
	}
}

func TestGCBranchScopeCollection(t *testing.T) {
	root := initRepo(t, `version: 1
docs:
  - path: "**/*.plan.md"
    visibility: public
    lifetime: ephemeral
    expire: { on: worktree, scope: branch }
`)
	write(t, root, "f.plan.md", "plan\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "init")

	// Opt-in only: the safe local sweep must leave branch-scoped docs alone.
	if err := runGC([]string{"--trigger", "all"}); err != nil {
		t.Fatalf("gc all: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "f.plan.md")); err != nil {
		t.Fatal("branch-scoped doc must survive the `all` sweep (opt-in only)")
	}

	// On a feature branch, branch collection must be a no-op (still in flight).
	git(t, root, "switch", "-c", "feature", "-q")
	if err := runGC([]string{"--trigger", "branch"}); err != nil {
		t.Fatalf("gc branch on feature: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "f.plan.md")); err != nil {
		t.Fatal("branch-scoped doc must survive on a feature branch")
	}

	// On the integration branch, its branch is considered merged → collect.
	git(t, root, "switch", "main", "-q")
	if err := runGC([]string{"--trigger", "branch"}); err != nil {
		t.Fatalf("gc branch on main: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "f.plan.md")); !os.IsNotExist(err) {
		t.Fatalf("branch-scoped doc must be collected on the integration branch, stat err=%v", err)
	}
}

func TestGCTTLUsesCommitDateNotMtime(t *testing.T) {
	root := initRepo(t, `version: 1
docs:
  - path: "r.md"
    visibility: public
    lifetime: ephemeral
    expire: { on: ttl, ttl_days: 30 }
`)
	write(t, root, "r.md", "old report\n")
	git(t, root, "add", "-A")
	// Commit 60 days ago, but leave a fresh mtime — the clone/checkout scenario.
	old := time.Now().AddDate(0, 0, -60).Format(time.RFC3339)
	gitEnv(t, root, []string{"GIT_AUTHOR_DATE=" + old, "GIT_COMMITTER_DATE=" + old}, "commit", "-qm", "old")
	now := time.Now()
	if err := os.Chtimes(filepath.Join(root, "r.md"), now, now); err != nil {
		t.Fatal(err)
	}

	if err := runGC([]string{"--trigger", "ttl"}); err != nil {
		t.Fatalf("gc ttl: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "r.md")); !os.IsNotExist(err) {
		t.Fatal("ttl must collect by commit date (60d old) despite a fresh mtime")
	}
}

func TestGCTTLCollectsGitignoredSensitiveFiles(t *testing.T) {
	// Sensitive ephemerals are gitignored, so git never lists them — the ttl
	// sweep must find them on disk or the "disk safety net" never fires.
	root := initRepo(t, `version: 1
docs:
  - path: "_scratch/**"
    visibility: private
    lifetime: ephemeral
    sensitive: true
    expire: { on: ttl, ttl_days: 30 }
`)
	write(t, root, ".gitignore", "_scratch/\n")
	write(t, root, "_scratch/notes.md", "old scratch\n")
	old := time.Now().AddDate(0, 0, -60)
	if err := os.Chtimes(filepath.Join(root, "_scratch/notes.md"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := runGC([]string{"--trigger", "ttl"}); err != nil {
		t.Fatalf("gc ttl: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "_scratch/notes.md")); !os.IsNotExist(err) {
		t.Fatalf("ttl must collect a gitignored sensitive file, stat err=%v", err)
	}
}

func TestGCTTLSparesUncommittedModifications(t *testing.T) {
	// git rm refuses a file whose worktree content differs from the index; that
	// refusal is a protection, and gc must not fall back to a plain delete.
	root := initRepo(t, `version: 1
docs:
  - path: "r.md"
    visibility: public
    lifetime: ephemeral
    expire: { on: ttl, ttl_days: 30 }
`)
	write(t, root, "r.md", "old report\n")
	git(t, root, "add", "-A")
	old := time.Now().AddDate(0, 0, -60).Format(time.RFC3339)
	gitEnv(t, root, []string{"GIT_AUTHOR_DATE=" + old, "GIT_COMMITTER_DATE=" + old}, "commit", "-qm", "old")
	write(t, root, "r.md", "old report\nplus uncommitted work\n")

	if err := runGC([]string{"--trigger", "ttl"}); err != nil {
		t.Fatalf("gc ttl: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "r.md"))
	if err != nil {
		t.Fatal("a ttl-expired file with uncommitted modifications must survive gc")
	}
	if string(data) != "old report\nplus uncommitted work\n" {
		t.Fatal("uncommitted content must be untouched")
	}
}

func TestGCTTLKeepsFreshDoc(t *testing.T) {
	root := initRepo(t, `version: 1
docs:
  - path: "r.md"
    visibility: public
    lifetime: ephemeral
    expire: { on: ttl, ttl_days: 30 }
`)
	write(t, root, "r.md", "fresh\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "fresh")
	if err := runGC([]string{"--trigger", "ttl"}); err != nil {
		t.Fatalf("gc ttl: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "r.md")); err != nil {
		t.Fatal("a recently committed doc must not be collected")
	}
}
