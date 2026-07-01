package cmd

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"golang.org/x/crypto/ssh"

	"github.com/rubenglez/doctier/internal/agex"
	"github.com/rubenglez/doctier/internal/config"
)

func TestCleanGuardsAgainstDoubleEncrypt(t *testing.T) {
	// Already-ciphertext input must pass through untouched (before recipients are
	// even loaded), so a keyless re-add can't double-encrypt and corrupt it.
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	recip, err := agessh.NewEd25519Recipient(sshPub)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := agex.Encrypt([]byte("already encrypted"), []age.Recipient{recip})
	if err != nil {
		t.Fatal(err)
	}
	out, err := clean(&config.Manifest{}, "/nonexistent", "f.md", ct)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(ct) {
		t.Fatal("clean must pass already-encrypted input through unchanged")
	}
}

func TestCleanEncryptsHybridArmorPlusPlaintext(t *testing.T) {
	// An armor block with plaintext appended must NOT ride the passthrough guard
	// into the index — encrypting the whole hybrid is the safe outcome.
	root := t.TempDir()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	recip, err := agessh.NewEd25519Recipient(sshPub)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".doctier"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".doctier/recipients.txt"), ssh.MarshalAuthorizedKey(sshPub), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCTIER_FORCE_ENCRYPT", "1") // skip the staged-blob reuse (no git repo here)

	ct, err := agex.Encrypt([]byte("secret"), []age.Recipient{recip})
	if err != nil {
		t.Fatal(err)
	}
	hybrid := append(append([]byte{}, ct...), []byte("\nplaintext notes below the armor\n")...)
	m := &config.Manifest{RecipientsFile: ".doctier/recipients.txt"}
	out, err := clean(m, root, "secret.md", hybrid)
	if err != nil {
		t.Fatalf("clean: %v", err)
	}
	if string(out) == string(hybrid) {
		t.Fatal("hybrid armor+plaintext must not pass through unencrypted")
	}
	if !agex.ValidCiphertext(out) {
		t.Fatal("clean output for a hybrid must be valid ciphertext")
	}
}

func TestSmudgePassesThroughPlaintext(t *testing.T) {
	pt := []byte("# a normal markdown doc\n")
	out, err := smudge("doc.md", pt)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(pt) {
		t.Fatal("smudge must pass non-ciphertext through unchanged")
	}
}

func TestCleanSmudgeRoundTrip(t *testing.T) {
	root := t.TempDir()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".doctier"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".doctier/recipients.txt"), ssh.MarshalAuthorizedKey(sshPub), 0o644); err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(root, "id")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCTIER_SSH_KEY", keyPath)
	t.Setenv("DOCTIER_FORCE_ENCRYPT", "1") // skip the staged-blob reuse (no git repo here)

	m := &config.Manifest{RecipientsFile: ".doctier/recipients.txt"}
	plaintext := []byte("TOP SECRET STRATEGY\n")

	ct, err := clean(m, root, "secret.md", plaintext)
	if err != nil {
		t.Fatalf("clean: %v", err)
	}
	if !agex.IsEncrypted(ct) {
		t.Fatal("clean output must be ciphertext")
	}
	got, err := smudge("secret.md", ct)
	if err != nil {
		t.Fatalf("smudge: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Fatalf("round-trip mismatch: got %q want %q", got, plaintext)
	}
}
