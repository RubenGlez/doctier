package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"filippo.io/age/agessh"

	"github.com/rubenglez/doctier/internal/agex"
)

// mergeFixture writes encrypted base/ours/theirs temp files for a private path
// and returns their paths plus the identity PEM to decrypt them.
func mergeFixture(t *testing.T, root, base, ours, theirs string) (o, a, b string, privPEM []byte) {
	t.Helper()
	privPEM, pubLine := keyPair(t)
	write(t, root, ".doctier/recipients.txt", pubLine+"\n")
	recip, err := agessh.ParseRecipient(pubLine)
	if err != nil {
		t.Fatal(err)
	}
	enc := func(name, plain string) string {
		ct, err := agex.Encrypt([]byte(plain), []age.Recipient{recip})
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, ct, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	return enc("O.tmp", base), enc("A.tmp", ours), enc("B.tmp", theirs), privPEM
}

// Two branches editing different lines of the same private doc must merge
// cleanly, and the driver's output must be CIPHERTEXT: git stores %A as the
// merged blob directly (no clean filter, no pre-commit hook on a merge
// commit), so plaintext output would put a cleartext blob in history.
func TestMergeDriverMergesEncryptedSidesCleanly(t *testing.T) {
	root := initRepo(t, privManifest)
	o, a, b, privPEM := mergeFixture(t, root,
		"line1\nline2\nline3\n",
		"line1 ours\nline2\nline3\n",
		"line1\nline2\nline3 theirs\n")
	t.Setenv("DOCTIER_SSH_KEY", "")
	t.Setenv("DOCTIER_IDENTITY", string(privPEM))

	if err := runMerge([]string{o, a, b, "secret/doc.md"}); err != nil {
		t.Fatalf("clean 3-way merge: %v", err)
	}
	got, err := os.ReadFile(a)
	if err != nil {
		t.Fatal(err)
	}
	if !agex.ValidCiphertext(got) {
		t.Fatalf("merged blob must be re-encrypted (git stores it verbatim), got:\n%s", got)
	}
	id, err := agex.LoadIdentity("")
	if err != nil {
		t.Fatal(err)
	}
	pt, err := agex.Decrypt(got, id)
	if err != nil {
		t.Fatal(err)
	}
	want := "line1 ours\nline2\nline3 theirs\n"
	if string(pt) != want {
		t.Fatalf("merged plaintext = %q, want %q", pt, want)
	}
}

// Real conflicts surface as PLAINTEXT conflict markers plus a non-zero exit,
// not interleaved base64 armor.
func TestMergeDriverLeavesPlaintextConflictMarkers(t *testing.T) {
	root := initRepo(t, privManifest)
	o, a, b, privPEM := mergeFixture(t, root,
		"line1\n", "line1 ours\n", "line1 theirs\n")
	t.Setenv("DOCTIER_SSH_KEY", "")
	t.Setenv("DOCTIER_IDENTITY", string(privPEM))

	err := runMerge([]string{o, a, b, "secret/doc.md"})
	if err == nil {
		t.Fatal("conflicting merge must return an error so git records the conflict")
	}
	got, readErr := os.ReadFile(a)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(got), "<<<<<<< ours") || !strings.Contains(string(got), ">>>>>>> theirs") {
		t.Fatalf("expected plaintext conflict markers, got:\n%s", got)
	}
	if agex.IsEncrypted(got) {
		t.Fatal("conflict output must be plaintext, not armor")
	}
}

// Without a key the driver must fail (conflict) with instructions and leave
// the current side untouched.
func TestMergeDriverFailsClosedWithoutKey(t *testing.T) {
	root := initRepo(t, privManifest)
	o, a, b, _ := mergeFixture(t, root, "base\n", "ours\n", "theirs\n")
	before, err := os.ReadFile(a)
	if err != nil {
		t.Fatal(err)
	}
	noKey(t)

	mergeErr := runMerge([]string{o, a, b, "secret/doc.md"})
	if mergeErr == nil {
		t.Fatal("keyless merge of an encrypted doc must fail")
	}
	if !strings.Contains(mergeErr.Error(), "--ours") {
		t.Fatalf("keyless error should tell the user how to pick a side, got: %v", mergeErr)
	}
	after, err := os.ReadFile(a)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("keyless merge must leave the current side untouched")
	}
}

// A non-private path merges as plain text without needing any key.
func TestMergeDriverPlainPathNeedsNoKey(t *testing.T) {
	root := initRepo(t, privManifest)
	noKey(t)
	writeTmp := func(name, content string) string {
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	o := writeTmp("O.tmp", "a\nb\nc\n")
	a := writeTmp("A.tmp", "a1\nb\nc\n")
	b := writeTmp("B.tmp", "a\nb\nc3\n")
	if err := runMerge([]string{o, a, b, "docs/notes.md"}); err != nil {
		t.Fatalf("plain path merge: %v", err)
	}
	got, err := os.ReadFile(a)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "a1\nb\nc3\n" {
		t.Fatalf("plain merge = %q", got)
	}
}

// init must wire the merge driver and attach merge=doctier in .gitattributes.
func TestInitConfiguresMergeDriver(t *testing.T) {
	root := initRepo(t, privManifest)
	_ = captureStdout(t, func() {
		if err := runInit(nil); err != nil {
			t.Fatalf("init: %v", err)
		}
	})
	if got := gitOut(t, root, "config", "--local", "merge.doctier.driver"); !strings.Contains(got, "doctier merge") {
		t.Fatalf("merge.doctier.driver = %q", got)
	}
	attrs, err := os.ReadFile(filepath.Join(root, ".gitattributes"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(attrs), "merge=doctier") {
		t.Fatalf(".gitattributes missing merge=doctier:\n%s", attrs)
	}
}
