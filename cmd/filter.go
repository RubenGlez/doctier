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
		out, err = smudge(file, input)
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
	force := os.Getenv("DOCTIER_FORCE_ENCRYPT") != ""
	// Handle ciphertext input (e.g. a keyless checkout left ciphertext in the
	// working tree via the fail-open smudge, and the file is being re-added). The
	// whole blob must validate: a mere armor-header prefix would let plaintext
	// appended below the block ride through to the index unencrypted.
	if agex.ValidCiphertext(plaintext) {
		if !force {
			// Normal add: pass through unchanged rather than double-encrypting.
			return plaintext, nil
		}
		// Forced re-encryption (grant / revoke to a changed recipient set) MUST
		// actually re-key the blob. Passing the old ciphertext through would leave
		// a revoked recipient able to read it while grant prints "re-encrypted" —
		// a silent security failure. Decrypt first, then fall through to encrypt to
		// the current recipients; failing to obtain a key is fatal (fail closed).
		id, err := agex.LoadIdentity("")
		if err != nil {
			return nil, fmt.Errorf("re-encrypt %s: a readable key is required to re-encrypt existing ciphertext: %w", file, err)
		}
		pt, err := agex.Decrypt(plaintext, id)
		if err != nil {
			return nil, fmt.Errorf("re-encrypt %s: %w", file, err)
		}
		plaintext = pt
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
	if !force {
		if existing := reuseCiphertext(file, plaintext); existing != nil {
			return existing, nil
		}
	}
	return agex.Encrypt(plaintext, recipients)
}

// runTextconv implements the diff.doctier.textconv driver: git hands it the
// path of a temp file holding one side of the diff (ciphertext for the blob
// side, already-smudged plaintext for the worktree side). Print plaintext when
// the local key can decrypt, else the raw bytes — the diff then shows armor,
// which is honest for a keyless machine.
func runTextconv(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("textconv: usage: doctier textconv <file>")
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	if agex.IsEncrypted(data) {
		if id, idErr := agex.LoadIdentity(""); idErr == nil {
			if pt, decErr := agex.Decrypt(data, id); decErr == nil {
				data = pt
			}
		}
	}
	_, err = os.Stdout.Write(data)
	return err
}

func smudge(file string, ciphertext []byte) ([]byte, error) {
	if !agex.IsEncrypted(ciphertext) {
		// Never encrypted (e.g. added before the filter was configured).
		return ciphertext, nil
	}
	// Fail open — emit ciphertext rather than failing the checkout — but never
	// silently: a passphrase-protected or missing key would otherwise degrade
	// with no signal at all. git relays filter stderr to the user.
	id, err := agex.LoadIdentity("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "doctier: %s left encrypted: %v\n", file, err)
		return ciphertext, nil
	}
	pt, err := agex.Decrypt(ciphertext, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "doctier: %s left encrypted: %v\n", file, err)
		return ciphertext, nil
	}
	return pt, nil
}
