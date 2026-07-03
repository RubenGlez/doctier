package cmd

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/rubenglez/doctier/internal/agex"
	"github.com/rubenglez/doctier/internal/config"
	"github.com/rubenglez/doctier/internal/gitx"
)

// runCheck enforces the policy fail-closed. It is meant for pre-commit /
// pre-push hooks and CI. Any violation returns a non-zero exit.
func runCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	staged := fs.Bool("staged", false, "check staged files only (default: all listed files)")
	push := fs.Bool("push", false, "validate the trees of commits being pushed (reads pre-push stdin)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	m, root, err := loadManifest()
	if err != nil {
		return err
	}

	// Pre-push mode validates what is actually being published (the pushed commit
	// trees) rather than the current index — the index may have been fixed since
	// the offending commit, or the pushed branch may not even be checked out.
	if *push {
		return checkPush(m, root)
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

// checkPush reads the pre-push stdin protocol
// ("<localref> <localsha> <remoteref> <remotesha>" per line) and verifies that
// every private path in each pushed tip tree is stored as valid ciphertext.
// Branch deletions (all-zero local sha) are skipped.
func checkPush(m *config.Manifest, root string) error {
	sc := bufio.NewScanner(os.Stdin)
	seen := make(map[string]bool)
	var problems []string
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 {
			continue
		}
		localSHA := fields[1]
		if strings.Trim(localSHA, "0") == "" {
			continue // branch deletion — nothing pushed
		}
		if seen[localSHA] {
			continue
		}
		seen[localSHA] = true
		problems = append(problems, checkTree(m, localSHA)...)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Printf("  ✗ %s\n", p)
		}
		return fmt.Errorf("%d policy violation(s) in pushed commits", len(problems))
	}
	fmt.Println("✓ doctier: pushed commits satisfy policy")
	return nil
}

// checkTree verifies the private paths in a single commit/tree are encrypted.
func checkTree(m *config.Manifest, ref string) []string {
	files, err := gitx.TreeFiles(ref)
	if err != nil {
		return []string{fmt.Sprintf("%s: cannot read tree: %v", ref[:min(len(ref), 12)], err)}
	}
	var problems []string
	for _, f := range files {
		rule, ok := m.Match(f)
		if !ok || !rule.Encrypted() {
			continue
		}
		blob, err := gitx.TreeBlob(ref, f)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s@%s: cannot read blob: %v", f, ref[:min(len(ref), 12)], err))
			continue
		}
		if !agex.ValidCiphertext(blob) {
			problems = append(problems, fmt.Sprintf("%s@%s: private file is cleartext in a pushed commit", f, ref[:min(len(ref), 12)]))
		}
	}
	return problems
}
