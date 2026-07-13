package cmd

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/rubenglez/doctier/internal/agex"
	"github.com/rubenglez/doctier/internal/config"
	"github.com/rubenglez/doctier/internal/gitx"
)

// runStatus prints the effective classification of each listed document.
func runStatus(args []string) error {
	fs := newFlagSet("status", `usage: doctier status

Show the effective classification (visibility, lifetime, storage, expiry) of
each document git sees, plus warnings when this clone is missing setup.`)
	if err := fs.Parse(args); err != nil {
		return err
	}
	m, _, err := loadManifest()
	if err != nil {
		return err
	}
	files, err := gitx.ListFiles()
	if err != nil {
		return err
	}
	sort.Strings(files)

	tracked, err := gitx.TrackedFiles()
	if err != nil {
		return err
	}
	isTracked := make(map[string]bool, len(tracked))
	for _, f := range tracked {
		isTracked[f] = true
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "DOCUMENT\tVISIBILITY\tLIFETIME\tSTORAGE\tEXPIRES")
	for _, f := range files {
		rule, _ := m.Effective(f)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			f, rule.Visibility, rule.Lifetime, storage(rule, isTracked[f]), expiry(rule))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	warnCloneSetup(m)
	return nil
}

// warnCloneSetup surfaces the two silent failure modes of a clone with private
// rules: the filter config and hooks live in .git (they do not travel with the
// repo), and a passphrase-protected key cannot decrypt. Warnings only — status
// stays usable on a keyless CI checkout.
func warnCloneSetup(m *config.Manifest) {
	encrypts := false
	for _, r := range m.Docs {
		if r.Encrypted() {
			encrypts = true
			break
		}
	}
	if !encrypts {
		return
	}
	if gitx.ConfigGet("filter.doctier.clean") == "" {
		fmt.Fprintln(os.Stderr, "⚠ clean/smudge filter not configured in this clone — private docs will not encrypt or decrypt here; run 'doctier init'")
	}
	if _, err := agex.LoadIdentity(""); err != nil {
		fmt.Fprintf(os.Stderr, "⚠ no usable SSH key for decryption (private docs stay ciphertext): %v\n", err)
	}
}

func storage(r config.Rule, tracked bool) string {
	switch {
	case r.LocalOnly():
		return "local (gitignored)"
	case !tracked:
		// Not in git yet. Report where it WILL land on the next add, not a present
		// state it does not have.
		if r.Encrypted() {
			return "untracked (will encrypt)"
		}
		return "untracked"
	case r.Encrypted():
		return "git (encrypted)"
	default:
		return "git (plaintext)"
	}
}

func expiry(r config.Rule) string {
	if r.Expire == nil {
		return "—"
	}
	if r.Expire.On == "ttl" {
		return fmt.Sprintf("ttl %dd", r.Expire.TTLDays)
	}
	return r.Expire.On
}
