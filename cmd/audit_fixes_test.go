package cmd

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"golang.org/x/crypto/ssh"

	"github.com/rubenglez/doctier/internal/agex"
	"github.com/rubenglez/doctier/internal/config"
)

// keyPair returns an inline OpenSSH private-key PEM and its authorized_keys
// recipient line.
func keyPair(t *testing.T) (privPEM []byte, pubLine string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(block), strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func setStdin(t *testing.T, s string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = r
	go func() { _, _ = w.WriteString(s); w.Close() }()
	t.Cleanup(func() { os.Stdin = old; r.Close() })
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return string(out)
}

// noKey points identity resolution at nothing so tests can't accidentally pick
// up a real ~/.ssh key on the host.
func noKey(t *testing.T) {
	t.Helper()
	t.Setenv("DOCTIER_SSH_KEY", filepath.Join(t.TempDir(), "absent"))
	t.Setenv("DOCTIER_IDENTITY", "")
	t.Setenv("HOME", t.TempDir())
}

const privManifest = `version: 1
docs:
  - path: "secret/**"
    visibility: private
    lifetime: durable
recipients_file: .doctier/recipients.txt
`

// C1: a non-ASCII private filename (which git C-quotes without -z) must still be
// matched and flagged as plaintext.
func TestCheckStagedCatchesNonASCIIPrivateFile(t *testing.T) {
	root := initRepo(t, privManifest)
	_, pubLine := keyPair(t)
	write(t, root, ".doctier/recipients.txt", pubLine+"\n")
	write(t, root, "secret/café.md", "TOP SECRET\n") // staged plaintext (no filter configured)
	git(t, root, "add", "secret/café.md", ".doctier/recipients.txt")
	if err := runCheck([]string{"--staged"}); err == nil {
		t.Fatal("check --staged must flag a plaintext private file even with a quoted (non-ASCII) name")
	}
}

// C2: forced re-encryption (revoke) of ciphertext input must fail closed when no
// key is available, instead of silently passing the old ciphertext through.
func TestCleanForceReencryptFailsClosedWithoutKey(t *testing.T) {
	root := t.TempDir()
	_, pubLine := keyPair(t)
	if err := os.MkdirAll(filepath.Join(root, ".doctier"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, root, ".doctier/recipients.txt", pubLine+"\n")
	recip, err := agessh.ParseRecipient(pubLine)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := agex.Encrypt([]byte("secret"), []age.Recipient{recip})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCTIER_FORCE_ENCRYPT", "1")
	noKey(t)
	m := &config.Manifest{RecipientsFile: ".doctier/recipients.txt"}
	if _, err := clean(m, root, "secret.md", ct); err == nil {
		t.Fatal("forced re-encryption of ciphertext without a key must fail closed, not pass through")
	}
}

// C3: an uncommitted file that merely matches a tracked ttl rule must not be
// deleted by the mtime fallback.
func TestGCDoesNotDeleteUncommittedTrackedFile(t *testing.T) {
	manifest := `version: 1
docs:
  - path: "tmp/**"
    visibility: public
    lifetime: ephemeral
    expire: { on: ttl, ttl_days: 1 }
`
	root := initRepo(t, manifest)
	write(t, root, "tmp/note.md", "x")
	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(filepath.Join(root, "tmp/note.md"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := runGC([]string{"--trigger", "ttl"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "tmp/note.md")); err != nil {
		t.Fatal("gc must not delete an uncommitted tracked-rule file by mtime")
	}
}

// C3: an expired local-only (sensitive) file is collected by quarantine, not an
// unrecoverable unlink.
func TestGCQuarantinesLocalOnlySensitiveFile(t *testing.T) {
	manifest := `version: 1
docs:
  - path: "scratch/**"
    visibility: private
    lifetime: ephemeral
    sensitive: true
    expire: { on: ttl, ttl_days: 1 }
`
	root := initRepo(t, manifest)
	write(t, root, "scratch/s.md", "secret scratch")
	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(filepath.Join(root, "scratch/s.md"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := runGC([]string{"--trigger", "ttl"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "scratch/s.md")); !os.IsNotExist(err) {
		t.Fatal("expired local-only file should leave its ttl-covered location")
	}
	if _, err := os.Stat(filepath.Join(root, ".doctier/trash/scratch/s.md")); err != nil {
		t.Fatal("expired local-only file should be quarantined, not unlinked")
	}
}

// H2: hooks must no-op in a repo with no manifest, so a shared/global hooks dir
// can't block every commit.
func TestHooksGuardOnManifest(t *testing.T) {
	for _, h := range []string{preCommitHook, prePushHook, postMergeHook} {
		if !strings.Contains(h, "[ -f .doctier.yml ] || exit 0") {
			t.Fatalf("hook missing manifest guard:\n%s", h)
		}
	}
}

// H5: check --push validates the pushed commit tree, catching cleartext even when
// the index has since been fixed.
func TestCheckPushCatchesCleartextInPushedCommit(t *testing.T) {
	root := initRepo(t, privManifest)
	_, pubLine := keyPair(t)
	write(t, root, ".doctier/recipients.txt", pubLine+"\n")
	write(t, root, "secret/plan.md", "TOP SECRET\n") // committed plaintext (no filter)
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "wip")
	sha := gitOut(t, root, "rev-parse", "HEAD")
	zero := strings.Repeat("0", 40)
	setStdin(t, "refs/heads/main "+sha+" refs/heads/main "+zero+"\n")
	if err := runCheck([]string{"--push"}); err == nil {
		t.Fatal("check --push must catch a cleartext private file in a pushed commit")
	}
}

// H4/H6: unlock decrypts private files into the worktree using an inline identity
// (the headless/agent path); cat prints one file's plaintext.
func TestUnlockAndCatWithInlineIdentity(t *testing.T) {
	root := initRepo(t, privManifest)
	privPEM, pubLine := keyPair(t)
	write(t, root, ".doctier/recipients.txt", pubLine+"\n")
	recip, err := agessh.ParseRecipient(pubLine)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := agex.Encrypt([]byte("PLAINTEXT BODY\n"), []age.Recipient{recip})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a keyless clone: ciphertext sits in both index and worktree.
	write(t, root, "secret/doc.md", string(ct))
	git(t, root, "add", "-A")

	t.Setenv("DOCTIER_SSH_KEY", "")
	t.Setenv("DOCTIER_IDENTITY", string(privPEM)) // inline PEM (H6)

	if err := runUnlock(nil); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "secret/doc.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "PLAINTEXT BODY\n" {
		t.Fatalf("unlock must decrypt into the worktree, got %q", got)
	}

	out := captureStdout(t, func() {
		if err := runCat([]string{"secret/doc.md"}); err != nil {
			t.Errorf("cat: %v", err)
		}
	})
	if out != "PLAINTEXT BODY\n" {
		t.Fatalf("cat must print plaintext, got %q", out)
	}
}

// M1: brace alternations expand to concrete gitattributes-safe patterns.
func TestExpandBraces(t *testing.T) {
	cases := map[string][]string{
		"docs/**":          {"docs/**"},
		"docs/{a,b}/**":    {"docs/a/**", "docs/b/**"},
		"x/{a,{b,c}}/y":    {"x/a/y", "x/b/y", "x/c/y"},
		"{one,two}.prd.md": {"one.prd.md", "two.prd.md"},
	}
	for in, want := range cases {
		if got := expandBraces(in); !reflect.DeepEqual(got, want) {
			t.Errorf("expandBraces(%q) = %v, want %v", in, got, want)
		}
	}
}

// M1 end-to-end: a brace rule produces one filter=doctier line per alternative in
// .gitattributes, not a single dead brace pattern.
func TestEnsureAttributesExpandsBraces(t *testing.T) {
	root := t.TempDir()
	m := &config.Manifest{Docs: []config.Rule{
		{Path: "docs/{a,b}/**", Visibility: "private", Lifetime: "durable"},
	}}
	if err := ensureAttributes(root, m); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".gitattributes"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"docs/a/** filter=doctier", "docs/b/** filter=doctier"} {
		if !strings.Contains(string(data), want) {
			t.Errorf(".gitattributes missing %q:\n%s", want, data)
		}
	}
	if strings.Contains(string(data), "{a,b}") {
		t.Errorf(".gitattributes must not contain a brace pattern git ignores:\n%s", data)
	}
}
