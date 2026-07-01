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

	// Not private-tracked → passthrough.
	if !ok || !rule.Encrypted() {
		_, err = os.Stdout.Write(input)
		return err
	}

	switch mode {
	case "clean":
		return clean(m, root, file, input)
	case "smudge":
		return smudge(input)
	default:
		return fmt.Errorf("filter: unknown mode %q", mode)
	}
}

func clean(m *config.Manifest, root, file string, plaintext []byte) error {
	recipients, err := agex.LoadRecipients(recipientsPath(m, root))
	if err != nil {
		return err
	}
	// Idempotency: age encryption is randomized, so re-encrypting identical
	// plaintext would churn the blob on every add. If the already-staged
	// ciphertext decrypts to the same plaintext, reuse it verbatim.
	// grant sets DOCTIER_FORCE_ENCRYPT to bypass reuse when the recipient set
	// changes and files must actually be re-encrypted.
	if os.Getenv("DOCTIER_FORCE_ENCRYPT") == "" {
		if existing := reuseCiphertext(file, plaintext); existing != nil {
			_, err = os.Stdout.Write(existing)
			return err
		}
	}
	ct, err := agex.Encrypt(plaintext, recipients)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(ct)
	return err
}

func smudge(ciphertext []byte) error {
	if !agex.IsEncrypted(ciphertext) {
		// Never encrypted (e.g. added before the filter was configured).
		_, err := os.Stdout.Write(ciphertext)
		return err
	}
	id, err := agex.LoadIdentity("")
	if err != nil {
		// No key on this machine: emit ciphertext so the file stays unreadable
		// rather than failing the checkout.
		_, werr := os.Stdout.Write(ciphertext)
		return werr
	}
	pt, err := agex.Decrypt(ciphertext, id)
	if err != nil {
		_, werr := os.Stdout.Write(ciphertext)
		return werr
	}
	_, err = os.Stdout.Write(pt)
	return err
}
