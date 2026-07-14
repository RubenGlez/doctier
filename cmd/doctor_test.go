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

// healthyRepo inits a repo with one private rule, grants a key, wires the full
// setup via runInit, and points identity resolution at the granted key. It
// returns the root and the recipient line so callers can stage encrypted blobs.
func healthyRepo(t *testing.T) (root, pubLine string) {
	t.Helper()
	root = initRepo(t, privManifest)
	var privPEM []byte
	privPEM, pubLine = keyPair(t)
	write(t, root, ".doctier/recipients.txt", pubLine+"\n")
	_ = captureStdout(t, func() {
		if err := runInit(nil); err != nil {
			t.Fatalf("init: %v", err)
		}
	})
	t.Setenv("DOCTIER_SSH_KEY", "")
	t.Setenv("DOCTIER_IDENTITY", string(privPEM))
	return root, pubLine
}

// stageRawBlob puts content into the index at rel verbatim, bypassing the clean
// filter (--no-filters). doctor reads what git actually stores, so tests must be
// able to plant both intact ciphertext and a plaintext leak directly.
func stageRawBlob(t *testing.T, root, rel string, content []byte) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
	sha := gitOut(t, root, "hash-object", "-w", "--no-filters", p)
	git(t, root, "update-index", "--add", "--cacheinfo", "100644,"+sha+","+rel)
}

func encryptTo(t *testing.T, pubLine, plain string) []byte {
	t.Helper()
	recip, err := agessh.ParseRecipient(pubLine)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := agex.Encrypt([]byte(plain), []age.Recipient{recip})
	if err != nil {
		t.Fatal(err)
	}
	return ct
}

// A fully wired repo with an intact, decryptable private blob passes and reports
// the file decrypting cleanly.
func TestDoctorHealthy(t *testing.T) {
	root, pubLine := healthyRepo(t)
	stageRawBlob(t, root, "secret/doc.md", encryptTo(t, pubLine, "top secret\n"))

	out := captureStdout(t, func() {
		if err := runDoctor(nil); err != nil {
			t.Fatalf("healthy repo must pass, got: %v", err)
		}
	})
	if !strings.Contains(out, "setup healthy") {
		t.Fatalf("expected a healthy summary, got:\n%s", out)
	}
	if !strings.Contains(out, "decrypt cleanly") {
		t.Fatalf("expected the private blob to be confirmed decryptable, got:\n%s", out)
	}
}

// The exact regression that motivated the command: a clone init'd before the
// merge driver landed has no merge.doctier.driver. doctor must fail on it.
func TestDoctorFlagsMissingMergeDriver(t *testing.T) {
	root, _ := healthyRepo(t)
	git(t, root, "config", "--local", "--unset", "merge.doctier.driver")

	out := captureStdout(t, func() {
		if err := runDoctor(nil); err == nil {
			t.Fatal("a missing merge driver must fail the doctor")
		}
	})
	if !strings.Contains(out, "merge.doctier.driver not set") {
		t.Fatalf("expected the missing merge driver to be named, got:\n%s", out)
	}
}

// A .gitattributes that carries the private pattern but not merge=doctier (the
// pre-rollout shape) is stale, not merely present. doctor must fail on it.
func TestDoctorFlagsStaleGitattributes(t *testing.T) {
	root, _ := healthyRepo(t)
	stale := blockBegin + "\nsecret/** filter=doctier diff=doctier\n" + blockEnd + "\n"
	if err := os.WriteFile(filepath.Join(root, ".gitattributes"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := runDoctor(nil); err == nil {
			t.Fatal("a stale .gitattributes (no merge=doctier) must fail the doctor")
		}
	})
	if !strings.Contains(out, "missing or stale") {
		t.Fatalf("expected the stale attribute line to be flagged, got:\n%s", out)
	}
}

// A plaintext blob stored at a private path is the worst case (a leak in git).
// doctor must catch it even when the working tree looks fine.
func TestDoctorFlagsPlaintextPrivateBlob(t *testing.T) {
	root, _ := healthyRepo(t)
	stageRawBlob(t, root, "secret/leak.md", []byte("PLAINTEXT LEAK\n"))

	out := captureStdout(t, func() {
		if err := runDoctor(nil); err == nil {
			t.Fatal("a plaintext private blob must fail the doctor")
		}
	})
	if !strings.Contains(out, "not valid age ciphertext") {
		t.Fatalf("expected the plaintext blob to be flagged, got:\n%s", out)
	}
}

// A missing hook is a hard failure: without pre-commit the fail-closed gate is
// gone.
func TestDoctorFlagsMissingHook(t *testing.T) {
	root, _ := healthyRepo(t)
	if err := os.Remove(filepath.Join(root, ".git", "hooks", "pre-commit")); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := runDoctor(nil); err == nil {
			t.Fatal("a missing pre-commit hook must fail the doctor")
		}
	})
	if !strings.Contains(out, "hook pre-commit missing") {
		t.Fatalf("expected the missing hook to be named, got:\n%s", out)
	}
}

// A public-only repo needs no encryption plumbing and no key: doctor must pass
// even with no key available, rather than demanding recipients it does not use.
func TestDoctorPublicOnlyNeedsNoKey(t *testing.T) {
	const publicManifest = `version: 1
docs:
  - path: "docs/**"
    visibility: public
    lifetime: durable
`
	root := initRepo(t, publicManifest)
	_ = captureStdout(t, func() {
		if err := runInit(nil); err != nil {
			t.Fatalf("init: %v", err)
		}
	})
	noKey(t)
	_ = root

	out := captureStdout(t, func() {
		if err := runDoctor(nil); err != nil {
			t.Fatalf("a public-only repo must pass without a key, got: %v", err)
		}
	})
	if !strings.Contains(out, "encryption plumbing not required") {
		t.Fatalf("expected the public-only path, got:\n%s", out)
	}
}
