package cmd

import (
	"flag"
	"fmt"

	"github.com/rubenglez/doctier/internal/agex"
	"github.com/rubenglez/doctier/internal/gitx"
)

// runCheck enforces the policy fail-closed. It is meant for pre-commit /
// pre-push hooks and CI. Any violation returns a non-zero exit.
func runCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	staged := fs.Bool("staged", false, "check staged files only (default: all listed files)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	m, root, err := loadManifest()
	if err != nil {
		return err
	}

	var files []string
	if *staged {
		files, err = gitx.StagedFiles()
	} else {
		files, err = gitx.ListFiles()
	}
	if err != nil {
		return err
	}

	var problems []string

	// If any rule encrypts, the recipients file must be usable — otherwise the
	// clean filter fails on the next add of a private file.
	for _, r := range m.Docs {
		if r.Encrypted() {
			if _, err := agex.LoadRecipients(recipientsPath(m, root)); err != nil {
				problems = append(problems, fmt.Sprintf("%s: unusable recipients file: %v", m.RecipientsFile, err))
			}
			break
		}
	}

	// Fetch every index blob the checks below need in one batch: the per-file
	// cat-file processes add up on large trees. A path absent from the map is
	// simply not in the index. LocalOnly and Encrypted are mutually exclusive.
	var lookup []string
	for _, f := range files {
		if rule, ok := m.Match(f); ok && ((rule.LocalOnly() && !*staged) || rule.Encrypted()) {
			lookup = append(lookup, f)
		}
	}
	blobs, err := gitx.StagedBlobs(lookup)
	if err != nil {
		return err
	}

	for _, f := range files {
		rule, ok := m.Match(f)
		if !ok {
			if m.Policy.Uncovered == "block" {
				problems = append(problems, fmt.Sprintf("%s: not covered by any rule (policy.uncovered=block)", f))
			}
			continue
		}
		// Sensitive ephemerals must never be committed. In --staged mode the file
		// list is already staged-only.
		if rule.LocalOnly() {
			if _, inIndex := blobs[f]; *staged || inIndex {
				problems = append(problems, fmt.Sprintf("%s: sensitive ephemeral must never be committed", f))
			}
			continue
		}
		// Private tracked files must be encrypted at rest in the index. Validate
		// the whole blob, not just the first line: plaintext appended after an
		// armor block (or smuggled inside one) must not pass as encrypted.
		if rule.Encrypted() {
			blob, inIndex := blobs[f]
			if !inIndex {
				continue // not staged yet; nothing to verify in the index
			}
			if !agex.ValidCiphertext(blob) {
				problems = append(problems, fmt.Sprintf("%s: private file is not valid age ciphertext in the index (cleartext, trailing data, or corrupted armor)", f))
			}
		}
	}

	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Printf("  ✗ %s\n", p)
		}
		return fmt.Errorf("%d policy violation(s)", len(problems))
	}
	fmt.Println("✓ doctier: policy satisfied")
	return nil
}
