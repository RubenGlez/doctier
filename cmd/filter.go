package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/rubenglez/doctier/internal/agex"
	"github.com/rubenglez/doctier/internal/config"
)

// runFilter implements the git clean/smudge filters:
//
//	clean  (working tree -> index):  plaintext in,  ciphertext out
//	smudge (index -> working tree):  ciphertext in, plaintext out
//
// Only paths whose rule is private+tracked are transformed; everything else is
// passed through untouched, so the same filter can be attached broadly.
func runFilter(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("filter: usage: doctier filter clean|smudge <file>")
	}
	mode, file := args[0], args[1]

	m, root, err := loadManifest()
	if err != nil {
		return err
	}
	rule, ok := m.Match(file)
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}

	var out []byte
	switch {
	case !ok || !rule.Encrypted():
		// Not private-tracked → passthrough.
		out = input
	case mode == "clean":
		out, err = clean(m, root, file, input)
	case mode == "smudge":
		out, err = smudge(input)
	default:
		return fmt.Errorf("filter: unknown mode %q", mode)
	}
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(out)
	return err
}

func clean(m *config.Manifest, root, file string, plaintext []byte) ([]byte, error) {
	// Guard against double-encryption: if the input is already age ciphertext
	// (e.g. a keyless checkout left ciphertext in the working tree via the
	// fail-open smudge, and the file is being re-added), pass it through
	// unchanged instead of encrypting it again and corrupting it.
	if agex.IsEncrypted(plaintext) {
		return plaintext, nil
	}
	recipients, err := agex.LoadRecipients(recipientsPath(m, root))
	if err != nil {
		return nil, err
	}
	// Idempotency: age encryption is randomized, so re-encrypting identical
	// plaintext would churn the blob on every add. If the already-staged
	// ciphertext decrypts to the same plaintext, reuse it verbatim.
	// grant sets DOCTIER_FORCE_ENCRYPT to bypass reuse when the recipient set
	// changes and files must actually be re-encrypted.
	if os.Getenv("DOCTIER_FORCE_ENCRYPT") == "" {
		if existing := reuseCiphertext(file, plaintext); existing != nil {
			return existing, nil
		}
	}
	return agex.Encrypt(plaintext, recipients)
}

func smudge(ciphertext []byte) ([]byte, error) {
	if !agex.IsEncrypted(ciphertext) {
		// Never encrypted (e.g. added before the filter was configured).
		return ciphertext, nil
	}
	id, err := agex.LoadIdentity("")
	if err != nil {
		// No key on this machine: emit ciphertext so the file stays unreadable
		// rather than failing the checkout.
		return ciphertext, nil
	}
	pt, err := agex.Decrypt(ciphertext, id)
	if err != nil {
		return ciphertext, nil
	}
	return pt, nil
}
