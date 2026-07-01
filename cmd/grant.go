package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"filippo.io/age/agessh"
)

// runGrant adds an SSH public key as a recipient and re-encrypts private docs so
// the new holder can read them. Revoking is the same minus the key: remove the
// line from the recipients file and re-run (grant with no key re-encrypts).
func runGrant(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf(`grant: usage: doctier grant "<ssh-public-key>"`)
	}
	pubkey := strings.TrimSpace(strings.Join(args, " "))
	if _, err := agessh.ParseRecipient(pubkey); err != nil {
		return fmt.Errorf("grant: not a valid SSH recipient: %w", err)
	}

	m, root, err := loadManifest()
	if err != nil {
		return err
	}
	recFile := recipientsPath(m, root)

	if err := appendRecipient(recFile, pubkey); err != nil {
		return err
	}
	fmt.Printf("✓ added recipient to %s\n", m.Visibility.Private.RecipientsFile)

	// Re-encrypt all private-tracked files to the new recipient set.
	if err := reencryptAll(root); err != nil {
		return err
	}
	fmt.Println("✓ re-encrypted private documents to the new recipient set")
	fmt.Println("→ review and commit the changes (recipients file + re-encrypted docs)")
	return nil
}

func appendRecipient(recFile, pubkey string) error {
	existing, _ := os.ReadFile(recFile)
	if strings.Contains(string(existing), pubkey) {
		return fmt.Errorf("recipient already present")
	}
	f, err := os.OpenFile(recFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}
	_, err = f.WriteString(pubkey + "\n")
	return err
}

// reencryptAll forces the clean filter to re-run over the whole tree so private
// files are re-encrypted to the current recipients.
func reencryptAll(root string) error {
	cmd := exec.Command("git", "add", "--renormalize", ".")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "DOCTIER_FORCE_ENCRYPT=1")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
