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

	m, _, err := loadManifest()
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
	for _, f := range files {
		rule, ok := m.Match(f)
		if !ok {
			if m.Policy.Uncovered == "block" {
				problems = append(problems, fmt.Sprintf("%s: not covered by any rule (policy.uncovered=block)", f))
			}
			continue
		}
		// Sensitive ephemerals must never be committed.
		if rule.LocalOnly() {
			if isStaged(f, *staged) {
				problems = append(problems, fmt.Sprintf("%s: sensitive ephemeral must never be committed", f))
			}
			continue
		}
		// Private tracked files must be encrypted at rest in the index.
		if rule.Encrypted() {
			blob, err := gitx.StagedBlob(f)
			if err != nil {
				// Not staged yet; nothing to verify in the index.
				continue
			}
			if !agex.IsEncrypted(blob) {
				problems = append(problems, fmt.Sprintf("%s: private file is staged in CLEARTEXT (filter not applied)", f))
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

// isStaged reports whether f should be treated as staged. In --staged mode the
// caller already filtered to staged files; otherwise we consult the index.
func isStaged(f string, stagedMode bool) bool {
	if stagedMode {
		return true
	}
	_, err := gitx.StagedBlob(f)
	return err == nil
}
