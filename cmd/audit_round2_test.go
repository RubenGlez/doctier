package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"filippo.io/age/agessh"

	"github.com/rubenglez/doctier/internal/agex"
	"github.com/rubenglez/doctier/internal/config"
)

// captureStderr redirects os.Stderr for the duration of fn and returns what was
// written (warnings from check land there).
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	fn()
	w.Close()
	os.Stderr = old
	out, _ := io.ReadAll(r)
	return string(out)
}

// M2: an unknown --trigger must error, not silently print "nothing to collect"
// (a typo'd cron job would otherwise never collect, forever).
func TestGCRejectsUnknownTrigger(t *testing.T) {
	initRepo(t, privManifest)
	if err := runGC([]string{"--trigger", "pr_merge"}); err == nil {
		t.Fatal("gc must reject an unknown --trigger value")
	}
	// A valid trigger still works.
	if err := runGC([]string{"--trigger", "ttl"}); err != nil {
		t.Fatalf("valid trigger must succeed: %v", err)
	}
}

// M3: a blob encrypted to a subset of the current recipients is flagged as not
// covering every current recipient — detectable from the ciphertext, no key.
func TestMissingRecipientsDetectsDrift(t *testing.T) {
	_, aLine := keyPair(t)
	_, bLine := keyPair(t)
	recipA, err := agessh.ParseRecipient(aLine)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := agex.Encrypt([]byte("secret"), []age.Recipient{recipA})
	if err != nil {
		t.Fatal(err)
	}
	// Current recipient set = {A, B}, but the blob is encrypted only to A.
	tags := map[string]string{}
	for _, line := range []string{aLine, bLine} {
		tag, err := agex.SSHRecipientTag(line)
		if err != nil {
			t.Fatal(err)
		}
		tags[tag] = line
	}
	missing := missingRecipients(ct, tags)
	if len(missing) != 1 || missing[0] != bLine {
		t.Fatalf("expected recipient B flagged as missing, got %v", missing)
	}
}

// M4: the revoke flow (grant with no key) re-encrypts only private paths and must
// not stage the user's unrelated dirty worktree.
func TestGrantRenormalizeOnlyPrivatePaths(t *testing.T) {
	root := initRepo(t, privManifest)
	_, pubLine := keyPair(t)
	write(t, root, ".doctier/recipients.txt", pubLine+"\n")
	write(t, root, "secret/plan.md", "TOP SECRET\n")
	write(t, root, "notes.md", "unrelated\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "init")

	// Dirty, unrelated modification the user has not staged.
	write(t, root, "notes.md", "unrelated WIP edit\n")

	if err := runGrant(nil); err != nil {
		t.Fatalf("grant (revoke flow): %v", err)
	}
	staged := gitOut(t, root, "diff", "--cached", "--name-only")
	if strings.Contains(staged, "notes.md") {
		t.Fatalf("grant must not stage the unrelated dirty file; staged:\n%s", staged)
	}
}

// L1: an untracked plaintext file at a private path is not a committed violation
// (check passes) but must be surfaced as a warning, not silently accepted.
func TestCheckWarnsOnUntrackedPlaintextPrivate(t *testing.T) {
	root := initRepo(t, privManifest)
	write(t, root, ".doctier/recipients.txt", pubKeyLine(t)+"\n")
	write(t, root, "secret/leaked.md", "PLAINTEXT EXPORT\n") // untracked, never added

	var err error
	out := captureStderr(t, func() { err = runCheck(nil) })
	if err != nil {
		t.Fatalf("untracked plaintext is not a committed violation; check must pass, got %v", err)
	}
	if !strings.Contains(out, "untracked plaintext") {
		t.Fatalf("check must warn about untracked plaintext at a private path; stderr:\n%s", out)
	}
}

// L3: init creates the recipients file at the manifest's recipients_file path,
// not the hardcoded default.
func TestInitHonorsRecipientsFile(t *testing.T) {
	manifest := `version: 1
docs:
  - path: "secret/**"
    visibility: private
    lifetime: durable
recipients_file: keys/recip.txt
`
	root := initRepo(t, manifest)
	if err := runInit(nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "keys/recip.txt")); err != nil {
		t.Fatalf("init must create the recipients file at the manifest path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".doctier/recipients.txt")); !os.IsNotExist(err) {
		t.Fatal("init must not create the hardcoded default when the manifest points elsewhere")
	}
}

// L4: an untracked file must not be reported as living in git storage.
func TestStatusStorageForUntracked(t *testing.T) {
	enc := config.Rule{Path: "secret/**", Visibility: "private", Lifetime: "durable"}
	pub := config.Rule{Path: "docs/**", Visibility: "public", Lifetime: "durable"}
	if got := storage(enc, false); got != "untracked (will encrypt)" {
		t.Errorf("untracked private = %q, want untracked (will encrypt)", got)
	}
	if got := storage(pub, false); got != "untracked" {
		t.Errorf("untracked public = %q, want untracked", got)
	}
	if got := storage(enc, true); got != "git (encrypted)" {
		t.Errorf("tracked private = %q, want git (encrypted)", got)
	}
}

// L8: a corrupted managed block (a stray end marker before begin) must be rewritten
// to a single clean block, not duplicated by falling through to the append path.
func TestEnsureBlockNoDuplicateOnCorruptedMarkers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitattributes")
	corrupt := blockEnd + "\n" + blockBegin + "\nold line\n" + blockEnd + "\n"
	if err := os.WriteFile(path, []byte(corrupt), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureBlock(path, []string{"new line"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), blockBegin); n != 1 {
		t.Fatalf("managed block must appear exactly once, found %d:\n%s", n, data)
	}
	if strings.Contains(string(data), "old line") {
		t.Fatalf("stale managed content must be replaced:\n%s", data)
	}
	if !strings.Contains(string(data), "new line") {
		t.Fatalf("new managed content missing:\n%s", data)
	}
}
