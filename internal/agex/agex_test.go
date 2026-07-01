package agex_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"golang.org/x/crypto/ssh"

	"github.com/rubenglez/doctier/internal/agex"
)

// keypair returns an age recipient/identity pair backed by a fresh ed25519 SSH
// key, plus the authorized_keys-format public line.
func keypair(t *testing.T) (age.Recipient, age.Identity, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
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
	id, err := agessh.NewEd25519Identity(priv)
	if err != nil {
		t.Fatal(err)
	}
	return recip, id, string(ssh.MarshalAuthorizedKey(sshPub))
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	recip, id, _ := keypair(t)
	plaintext := []byte("TOP SECRET STRATEGY\n")

	ct, err := agex.Encrypt(plaintext, []age.Recipient{recip})
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !agex.IsEncrypted(ct) {
		t.Fatal("ciphertext should be detected as encrypted")
	}
	if agex.IsEncrypted(plaintext) {
		t.Fatal("plaintext should not be detected as encrypted")
	}
	pt, err := agex.Decrypt(ct, id)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(pt) != string(plaintext) {
		t.Fatalf("round-trip mismatch: got %q want %q", pt, plaintext)
	}
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	recip, _, _ := keypair(t)
	_, otherID, _ := keypair(t)
	ct, err := agex.Encrypt([]byte("secret"), []age.Recipient{recip})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agex.Decrypt(ct, otherID); err == nil {
		t.Fatal("decrypt with a non-recipient key must fail")
	}
}

func TestIsEncrypted(t *testing.T) {
	if !agex.IsEncrypted([]byte("\n  " + agex.ArmorHeader + "\nrest")) {
		t.Error("should detect armor header after leading whitespace")
	}
	if agex.IsEncrypted([]byte("# a normal markdown doc")) {
		t.Error("plain text must not be detected as encrypted")
	}
}

func TestLoadRecipients(t *testing.T) {
	_, _, keyLine := keypair(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "recipients.txt")
	content := "# a comment\n\n" + keyLine + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	rs, err := agex.LoadRecipients(path)
	if err != nil {
		t.Fatalf("load recipients: %v", err)
	}
	if len(rs) != 1 {
		t.Fatalf("expected 1 recipient (comments/blanks skipped), got %d", len(rs))
	}

	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, []byte("# only a comment\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := agex.LoadRecipients(empty); err == nil {
		t.Fatal("a recipients file with no keys must error")
	}
}
