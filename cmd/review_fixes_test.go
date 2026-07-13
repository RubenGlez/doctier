package cmd

// Regression tests for the 2026-07 review fixes: subdirectory anchoring,
// worktree hooks, push-net coverage of sensitive ephemerals, unlock safety,
// gc exit codes, gitignore brace expansion, trash re-collection, -h handling
// and agents block splicing.

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"filippo.io/age/agessh"

	"github.com/rubenglez/doctier/internal/agex"
	"github.com/rubenglez/doctier/internal/config"
)

// A fail-closed check run from a subdirectory must see the same repo-relative
// paths as one run from the root — before the cwd anchoring fix it silently
// passed over committed cleartext.
func TestCheckFromSubdirectoryCatchesCleartext(t *testing.T) {
	root := initRepo(t, privManifest)
	_, pubLine := keyPair(t)
	write(t, root, ".doctier/recipients.txt", pubLine+"\n")
	write(t, root, "secret/plan.md", "TOP SECRET\n") // committed plaintext (no filter)
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "wip")

	if err := runCheck(nil); err == nil {
		t.Fatal("sanity: check from the root must flag the cleartext private file")
	}
	t.Chdir(filepath.Join(root, "secret"))
	if err := runCheck(nil); err == nil {
		t.Fatal("check from a subdirectory must flag the same cleartext private file")
	}
}

// init inside a linked worktree must install hooks where git reads them (the
// common .git/hooks), not the per-worktree .git/worktrees/<name>/hooks.
func TestInitInWorktreeInstallsHooksInCommonDir(t *testing.T) {
	root := initRepo(t, privManifest)
	git(t, root, "add", ".doctier.yml")
	git(t, root, "commit", "-q", "-m", "manifest")
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, root, "worktree", "add", "-q", "-b", "wt-branch", wt)

	t.Chdir(wt)
	if err := runInit(nil); err != nil {
		t.Fatalf("init in worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "hooks", "pre-commit")); err != nil {
		t.Fatal("init in a linked worktree must install hooks into the common .git/hooks")
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "worktrees", "wt", "hooks", "pre-commit")); err == nil {
		t.Fatal("hooks must not land in the per-worktree gitdir, where git never reads them")
	}
}

// The push net must flag a committed sensitive ephemeral (e.g. a --no-verify
// commit), not just cleartext private files.
func TestCheckPushCatchesCommittedSensitiveEphemeral(t *testing.T) {
	manifest := `version: 1
docs:
  - path: "_scratch/**"
    visibility: private
    lifetime: ephemeral
    sensitive: true
`
	root := initRepo(t, manifest)
	write(t, root, "_scratch/notes.md", "never commit me\n")
	git(t, root, "add", "-A") // no init ran, so nothing gitignores it
	git(t, root, "commit", "-q", "-m", "oops")
	sha := gitOut(t, root, "rev-parse", "HEAD")
	zero := strings.Repeat("0", 40)
	setStdin(t, "refs/heads/main "+sha+" refs/heads/main "+zero+"\n")
	if err := runCheck([]string{"--push"}); err == nil {
		t.Fatal("check --push must flag a committed sensitive ephemeral")
	}
}

// unlock must never overwrite a worktree copy that is already plaintext — it
// may hold uncommitted work.
func TestUnlockDoesNotClobberPlaintextEdits(t *testing.T) {
	root := initRepo(t, privManifest)
	privPEM, pubLine := keyPair(t)
	write(t, root, ".doctier/recipients.txt", pubLine+"\n")
	recip, err := agessh.ParseRecipient(pubLine)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := agex.Encrypt([]byte("v1 committed\n"), []age.Recipient{recip})
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, "secret/doc.md", string(ct))
	git(t, root, "add", "-A")
	// The user edited the decrypted file but has not staged the change.
	write(t, root, "secret/doc.md", "v2 uncommitted work\n")

	t.Setenv("DOCTIER_SSH_KEY", "")
	t.Setenv("DOCTIER_IDENTITY", string(privPEM))
	if err := runUnlock(nil); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "secret/doc.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v2 uncommitted work\n" {
		t.Fatalf("unlock overwrote uncommitted plaintext edits, got %q", got)
	}
}

// unlock writes decrypted (deliberately-encrypted-at-rest) content owner-only.
func TestUnlockWritesOwnerOnlyPermissions(t *testing.T) {
	root := initRepo(t, privManifest)
	privPEM, pubLine := keyPair(t)
	write(t, root, ".doctier/recipients.txt", pubLine+"\n")
	recip, err := agessh.ParseRecipient(pubLine)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := agex.Encrypt([]byte("body\n"), []age.Recipient{recip})
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, "secret/doc.md", string(ct))
	git(t, root, "add", "-A")

	t.Setenv("DOCTIER_SSH_KEY", "")
	t.Setenv("DOCTIER_IDENTITY", string(privPEM))
	if err := runUnlock(nil); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	info, err := os.Stat(filepath.Join(root, "secret/doc.md"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("unlock must write 0600, got %o", perm)
	}
}

// gc must exit non-zero when a removal genuinely fails, instead of printing
// "collected" and returning success forever (e.g. from a cron job). A blocked
// quarantine (`.doctier/trash` existing as a file) is such a failure.
func TestGCReportsFailureWhenRemovalFails(t *testing.T) {
	manifest := `version: 1
docs:
  - path: "scratch/**"
    visibility: private
    lifetime: ephemeral
    sensitive: true
    expire: { on: ttl, ttl_days: 1 }
`
	root := initRepo(t, manifest)
	write(t, root, "scratch/s.md", "expired scratch")
	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(filepath.Join(root, "scratch/s.md"), old, old); err != nil {
		t.Fatal(err)
	}
	// A file squatting on the quarantine path makes the move fail.
	write(t, root, ".doctier/trash", "")

	var err error
	_ = captureStderr(t, func() {
		_ = captureStdout(t, func() { err = runGC([]string{"--trigger", "ttl"}) })
	})
	if err == nil {
		t.Fatal("gc must return an error when a removal fails")
	}
	if _, statErr := os.Stat(filepath.Join(root, "scratch/s.md")); statErr != nil {
		t.Fatal("the file whose removal failed must still exist")
	}
}

// Integration triggers only collect tracked docs; an untracked in-flight file
// must be skipped, not counted as "collected" with a failing git rm behind it.
func TestGCIntegrationSkipsUntrackedFiles(t *testing.T) {
	manifest := `version: 1
docs:
  - path: "**/*.prd.md"
    visibility: public
    lifetime: ephemeral
    expire: { on: pr-merge }
`
	root := initRepo(t, manifest)
	write(t, root, "README.md", "x")
	git(t, root, "add", "README.md")
	git(t, root, "commit", "-q", "-m", "init") // main now exists for DefaultBranch
	write(t, root, "feature.prd.md", "in flight, never committed")

	if err := runGC([]string{"--trigger", "pr-merge"}); err != nil {
		t.Fatalf("gc must not fail on an untracked in-flight ephemeral: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "feature.prd.md")); err != nil {
		t.Fatal("an untracked ephemeral must not be collected")
	}
}

// Sensitive-rule brace patterns must expand into gitignore-safe lines, exactly
// like .gitattributes patterns do.
func TestEnsureIgnoresExpandsBraces(t *testing.T) {
	root := t.TempDir()
	m := &config.Manifest{Docs: []config.Rule{
		{Path: "{tmp,notes}/**", Visibility: "private", Lifetime: "ephemeral",
			Sensitive: true, Expire: &config.Expire{On: "worktree", Scope: "worktree"}},
	}}
	if err := ensureIgnores(root, m); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"tmp/**", "notes/**"} {
		if !strings.Contains(string(data), want) {
			t.Errorf(".gitignore missing %q:\n%s", want, data)
		}
	}
	if strings.Contains(string(data), "{tmp,notes}") {
		t.Errorf(".gitignore must not contain a brace pattern git ignores:\n%s", data)
	}
}

// A quarantined file must rest in .doctier/trash — not be re-matched by its
// `**`-anchored rule and nested one level deeper on every sweep.
func TestGCTTLDoesNotRecollectQuarantinedFiles(t *testing.T) {
	manifest := `version: 1
docs:
  - path: "**/_scratch/**"
    visibility: private
    lifetime: ephemeral
    sensitive: true
    expire: { on: ttl, ttl_days: 1 }
`
	root := initRepo(t, manifest)
	trashed := filepath.Join(root, ".doctier/trash/_scratch/notes.md")
	write(t, root, ".doctier/trash/_scratch/notes.md", "already collected")
	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(trashed, old, old); err != nil {
		t.Fatal(err)
	}
	if err := runGC([]string{"--trigger", "ttl"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(trashed); err != nil {
		t.Fatal("a quarantined file must stay where it is")
	}
	nested := filepath.Join(root, ".doctier/trash/.doctier/trash/_scratch/notes.md")
	if _, err := os.Stat(nested); err == nil {
		t.Fatal("gc must not re-quarantine trash into a nested trash path")
	}
}

// "doctier init -h" is a help request: it must not scaffold anything.
func TestInitHelpDoesNotScaffold(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q", "-b", "main")
	t.Chdir(root)
	var err error
	_ = captureStderr(t, func() { err = runInit([]string{"-h"}) })
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("init -h should surface flag.ErrHelp, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".doctier.yml")); !os.IsNotExist(statErr) {
		t.Fatal("init -h must not create .doctier.yml")
	}
	if _, statErr := os.Stat(filepath.Join(root, ".git", "hooks", "pre-commit")); !os.IsNotExist(statErr) {
		t.Fatal("init -h must not install hooks")
	}
}

// Execute maps flag.ErrHelp to exit 0 for every subcommand.
func TestExecuteHelpExitsZero(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q", "-b", "main")
	t.Chdir(root)
	for _, cmd := range []string{"init", "check", "status", "agents", "gc", "grant", "unlock", "cat"} {
		var code int
		_ = captureStderr(t, func() {
			code = Execute([]string{cmd, "-h"})
		})
		if code != 0 {
			t.Errorf("doctier %s -h: exit %d, want 0", cmd, code)
		}
	}
}

// A stray end marker before the begin marker must not duplicate the agents
// managed block (same hardening as init's ensureBlock).
func TestAgentsWriteBlockNoDuplicateOnCorruptedMarkers(t *testing.T) {
	root := t.TempDir()
	corrupted := agentsEnd + "\nsome text\n" + agentsBegin + "\nold\n" + agentsEnd + "\ntrailer\n"
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(corrupted), 0o644); err != nil {
		t.Fatal(err)
	}
	block := agentsBegin + "\nnew\n" + agentsEnd + "\n"
	_ = captureStdout(t, func() {
		if err := writeBlock(root, "AGENTS.md", block); err != nil {
			t.Fatal(err)
		}
	})
	data, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), agentsBegin); got != 1 {
		t.Fatalf("managed block duplicated: %d begin markers\n%s", got, data)
	}
	if !strings.Contains(string(data), "trailer") {
		t.Fatalf("content after the end marker must be preserved:\n%s", data)
	}
}

// policy.uncovered=warn must actually warn instead of silently allowing.
func TestCheckWarnsOnUncoveredWithWarnPolicy(t *testing.T) {
	manifest := `version: 1
docs:
  - path: "docs/**"
    visibility: public
    lifetime: durable
policy:
  uncovered: warn
`
	root := initRepo(t, manifest)
	write(t, root, "stray.md", "unclassified")
	git(t, root, "add", "stray.md")
	var err error
	stderr := captureStderr(t, func() {
		_ = captureStdout(t, func() { err = runCheck(nil) })
	})
	if err != nil {
		t.Fatalf("warn policy must not fail the check: %v", err)
	}
	if !strings.Contains(stderr, "stray.md") || !strings.Contains(stderr, "not covered") {
		t.Fatalf("expected an uncovered warning for stray.md, got:\n%s", stderr)
	}
}
