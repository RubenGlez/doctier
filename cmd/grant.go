package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"filippo.io/age/agessh"
	"github.com/rubenglez/doctier/internal/config"
	"github.com/rubenglez/doctier/internal/gitx"
)

// runGrant adds an SSH public key as a recipient and re-encrypts private docs so
// the new holder can read them. Revoking is the same minus the key: remove the
// line from the recipients file and re-run `doctier grant` with no argument,
// which just re-encrypts to the current recipient set.
func runGrant(args []string) error {
	m, root, err := loadManifest()
	if err != nil {
		return err
	}

	if len(args) > 0 {
		pubkey := strings.TrimSpace(strings.Join(args, " "))
		if _, err := agessh.ParseRecipient(pubkey); err != nil {
			return fmt.Errorf("grant: not a valid SSH recipient: %w", err)
		}
		if err := appendRecipient(recipientsPath(m, root), pubkey); err != nil {
			return err
		}
		fmt.Printf("✓ added recipient to %s\n", m.RecipientsFile)
	}

	// Re-encrypt all private-tracked files to the current recipient set. With no
	// key argument this is the whole command — the revoke flow: remove the line
	// from the recipients file, then run `doctier grant`.
	if err := reencryptAll(m, root); err != nil {
		return err
	}
	fmt.Println("✓ re-encrypted private documents to the current recipient set")
	fmt.Println("→ review and commit the changes (recipients file + re-encrypted docs)")
	if len(args) == 0 {
		fmt.Println("  note: a removed recipient can still read every version already in git history")
	}
	return nil
}

func appendRecipient(recFile, pubkey string) error {
	existing, _ := os.ReadFile(recFile)
	// Compare whole lines: a substring match would count a commented-out key as
	// present, and a key line usually ends in a free-form comment field anyway.
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == pubkey {
			return fmt.Errorf("recipient already present")
		}
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

// reencryptAll forces the clean filter to re-run over the private-tracked files
// so they are re-encrypted to the current recipients. It scopes the renormalize
// to those pathspecs rather than `git add --renormalize .`, which would sweep the
// user's unrelated dirty worktree into the recipients commit.
func reencryptAll(m *config.Manifest, root string) error {
	files, err := gitx.TrackedFiles()
	if err != nil {
		return err
	}
	var paths []string
	for _, f := range files {
		if rule, ok := m.Match(f); ok && rule.Encrypted() {
			paths = append(paths, f)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"add", "--renormalize", "--"}, paths...)
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "DOCTIER_FORCE_ENCRYPT=1")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
