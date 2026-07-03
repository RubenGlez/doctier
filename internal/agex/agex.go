// Package agex wraps age encryption using SSH keys as recipients/identities.
//
// Recipients are reused SSH public keys (age supports ssh-ed25519 and ssh-rsa),
// so there is no separate key ceremony. Ciphertext is ASCII-armored so it is
// easy to detect in git and degrades gracefully in diffs.
package agex

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/rsa"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"filippo.io/age/armor"
	"golang.org/x/crypto/ssh"
)

// ArmorHeader is the first line of an armored age file.
const ArmorHeader = "-----BEGIN AGE ENCRYPTED FILE-----"

// ageMagic is the first line of the binary age format inside the armor.
const ageMagic = "age-encryption.org/v1\n"

// IsEncrypted reports whether data looks like armored age ciphertext. It only
// sniffs the first line — enough to decide whether to *attempt* decryption, but
// not a guarantee (use ValidCiphertext to verify a blob is really encrypted).
func IsEncrypted(data []byte) bool {
	return bytes.HasPrefix(bytes.TrimLeft(data, " \t\r\n"), []byte(ArmorHeader))
}

// ValidCiphertext reports whether data is, in its entirety, well-formed armored
// age ciphertext: a single armor block with nothing after it, whose payload is
// the age v1 format. A prefix sniff is not enough for the fail-closed check —
// plaintext appended after (or smuggled inside) an armor block must not pass as
// encrypted.
func ValidCiphertext(data []byte) bool {
	if !IsEncrypted(data) {
		return false
	}
	payload, err := io.ReadAll(armor.NewReader(bytes.NewReader(data)))
	if err != nil {
		return false // truncated armor, bad base64, or trailing data
	}
	return bytes.HasPrefix(payload, []byte(ageMagic))
}

// LoadRecipients parses an SSH-public-key-per-line recipients file. Blank lines
// and lines starting with '#' are ignored.
func LoadRecipients(path string) ([]age.Recipient, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open recipients: %w", err)
	}
	defer f.Close()

	var recipients []age.Recipient
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r, err := agessh.ParseRecipient(line)
		if err != nil {
			return nil, fmt.Errorf("parse recipient %q: %w", line, err)
		}
		recipients = append(recipients, r)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(recipients) == 0 {
		return nil, fmt.Errorf("no recipients found in %s", path)
	}
	return recipients, nil
}

// LoadIdentity loads an SSH private key as an age identity for decryption.
// Resolution order when keyPath is empty: $DOCTIER_SSH_KEY, then $DOCTIER_IDENTITY,
// then the usual default keys (~/.ssh/id_ed25519, ~/.ssh/id_rsa).
//
// The value may be either a filesystem path OR inline PEM key material. Inline
// material matters for headless/agent runs: CI and cloud-agent secrets are
// injected as values, not files, so `DOCTIER_IDENTITY="$(cat key)"` must work
// without the caller first writing a temp file. Anything containing a PEM header
// is treated as key bytes, everything else as a path.
func LoadIdentity(keyPath string) (age.Identity, error) {
	if keyPath == "" {
		keyPath = os.Getenv("DOCTIER_SSH_KEY")
	}
	if keyPath == "" {
		keyPath = os.Getenv("DOCTIER_IDENTITY")
	}
	if strings.Contains(keyPath, "-----BEGIN") {
		return identityFromPEM([]byte(keyPath))
	}
	candidates := []string{keyPath}
	if keyPath == "" {
		home, _ := os.UserHomeDir()
		candidates = []string{
			filepath.Join(home, ".ssh", "id_ed25519"),
			filepath.Join(home, ".ssh", "id_rsa"),
		}
	}
	var lastErr error
	for _, p := range candidates {
		if p == "" {
			continue
		}
		pem, err := os.ReadFile(p)
		if err != nil {
			lastErr = err
			continue
		}
		id, err := identityFromPEM(pem)
		if err != nil {
			lastErr = err
			continue
		}
		return id, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no SSH private key found")
	}
	return nil, lastErr
}

func identityFromPEM(pem []byte) (age.Identity, error) {
	k, err := ssh.ParseRawPrivateKey(pem)
	if err != nil {
		// A passphrase-protected key would otherwise fail with an opaque parse
		// error and the smudge filter would silently fall back to ciphertext;
		// name the problem so callers can surface it.
		var missing *ssh.PassphraseMissingError
		if errors.As(err, &missing) {
			return nil, fmt.Errorf("ssh key is passphrase-protected; doctier cannot use it non-interactively — point $DOCTIER_SSH_KEY at a passphrase-less key")
		}
		return nil, fmt.Errorf("parse ssh key: %w", err)
	}
	switch key := k.(type) {
	case *ed25519.PrivateKey:
		return agessh.NewEd25519Identity(*key)
	case ed25519.PrivateKey:
		return agessh.NewEd25519Identity(key)
	case *rsa.PrivateKey:
		return agessh.NewRSAIdentity(key)
	default:
		return nil, fmt.Errorf("unsupported ssh key type %T (use ed25519 or rsa)", k)
	}
}

// Encrypt returns armored age ciphertext of plaintext for the given recipients.
func Encrypt(plaintext []byte, recipients []age.Recipient) ([]byte, error) {
	var out bytes.Buffer
	armorW := armor.NewWriter(&out)
	w, err := age.Encrypt(armorW, recipients...)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(plaintext); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	if err := armorW.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Decrypt returns the plaintext of armored age ciphertext using identity.
func Decrypt(ciphertext []byte, identity age.Identity) ([]byte, error) {
	armorR := armor.NewReader(bytes.NewReader(ciphertext))
	r, err := age.Decrypt(armorR, identity)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}
