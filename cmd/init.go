package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rubenglez/doctier/internal/config"
	"github.com/rubenglez/doctier/internal/gitx"
)

const manifestTemplate = `version: 1

# doctier only needs rules for the EXCEPTIONS. A document that matches no rule is
# public + durable — plain git's default (tracked plaintext, kept forever). Two
# independent axes per glob, first matching rule wins:
#   visibility: public | private        (private is encrypted with age)
#   lifetime:   durable | ephemeral     (ephemeral = finite life, auto-deleted)
docs:
  # Example: product strategy -> private + durable (encrypted, tracked forever)
  # - path: "docs/strategy/**"
  #   visibility: private
  #   lifetime: durable

  # Example: a PRD that travels in the PR and disappears when it merges
  # - path: "**/*.prd.md"
  #   visibility: public
  #   lifetime: ephemeral
  #   expire: { on: pr-merge }

  # Example: sensitive scratch, never committed, local to the worktree.
  # sensitive defaults to expire on worktree removal; add expire: { on: ttl,
  # ttl_days: N } instead for a disk safety net.
  # - path: "**/_scratch/**"
  #   visibility: private
  #   lifetime: ephemeral
  #   sensitive: true

# Who can read private (encrypted) docs — an SSH-public-key-per-line file,
# managed with 'doctier grant'.
recipients_file: .doctier/recipients.txt

# ephemeral behaviour. integration_branch is where pr-merge ephemerals are
# collected; omit to auto-detect (origin/HEAD, else main/master).
# ephemeral:
#   integration_branch: main

# Optional strictness: refuse to commit any file that matches no rule above,
# forcing every document to be classified explicitly. Default: allow.
# policy:
#   uncovered: block
`

const recipientsTemplate = `# doctier recipients — one SSH public key per line (age reuses SSH keys).
# Example:
#   ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA... alice@example.com
`

const preCommitHook = `#!/usr/bin/env sh
# doctier: fail-closed policy check on staged files.
exec doctier check --staged
`

const prePushHook = `#!/usr/bin/env sh
# doctier: fail-closed policy check on the full tree before publishing — the
# reinforcement net for commits made with --no-verify or before 'doctier init'.
exec doctier check
`

const postMergeHook = `#!/usr/bin/env sh
# doctier: collect pr-merge ephemerals after an integrating merge. This is a no-op
# unless the current branch is the integration branch, so it is safe on a routine
# 'git pull' of a feature branch.
exec doctier gc --trigger pr-merge
`

// runInit scaffolds the manifest, recipients file, git attributes, ignore
// entries, clean/smudge filters and hooks. It is idempotent.
func runInit(args []string) error {
	root, err := gitx.Root()
	if err != nil {
		return fmt.Errorf("not inside a git repository: %w", err)
	}

	// 1. Manifest
	created, err := writeIfAbsent(filepath.Join(root, config.DefaultPath), manifestTemplate, 0o644)
	if err != nil {
		return err
	}
	report(created, config.DefaultPath)

	// 2. Recipients file
	recRel := ".doctier/recipients.txt"
	if err := os.MkdirAll(filepath.Join(root, ".doctier"), 0o755); err != nil {
		return err
	}
	created, err = writeIfAbsent(filepath.Join(root, recRel), recipientsTemplate, 0o644)
	if err != nil {
		return err
	}
	report(created, recRel)

	// 3. Load manifest so we can derive attributes/ignores from the rules.
	m, err := config.Load(filepath.Join(root, config.DefaultPath))
	if err != nil {
		return err
	}

	// 4. .gitattributes for private-tracked rules
	if err := ensureAttributes(root, m); err != nil {
		return err
	}

	// 5. .gitignore for local-only (sensitive ephemeral) rules
	if err := ensureIgnores(root, m); err != nil {
		return err
	}

	// 6. clean/smudge filters
	if err := configureFilters(); err != nil {
		return err
	}
	fmt.Println("✓ configured git filter.doctier (clean/smudge)")

	// 7. hooks
	if err := installHooks(); err != nil {
		return err
	}

	fmt.Println("\nNext steps:")
	fmt.Println("  1. Add recipients:  doctier grant \"$(cat ~/.ssh/id_ed25519.pub)\"")
	fmt.Println("  2. Edit .doctier.yml to classify your documents, then re-run")
	fmt.Println("     'doctier init' to sync .gitattributes/.gitignore with the rules.")
	fmt.Println("  3. Verify:          doctier check")
	return nil
}

func ensureAttributes(root string, m *config.Manifest) error {
	var lines []string
	for _, r := range m.Docs {
		if r.Visibility == "private" && !r.LocalOnly() {
			lines = append(lines, fmt.Sprintf("%s filter=doctier diff=doctier", r.Path))
		}
	}
	return ensureBlock(filepath.Join(root, ".gitattributes"), lines)
}

func ensureIgnores(root string, m *config.Manifest) error {
	var lines []string
	for _, r := range m.Docs {
		if r.LocalOnly() {
			lines = append(lines, r.Path)
		}
	}
	return ensureBlock(filepath.Join(root, ".gitignore"), lines)
}

func configureFilters() error {
	if err := gitx.ConfigSet("filter.doctier.clean", "doctier filter clean %f"); err != nil {
		return err
	}
	if err := gitx.ConfigSet("filter.doctier.smudge", "doctier filter smudge %f"); err != nil {
		return err
	}
	if err := gitx.ConfigSet("filter.doctier.required", "true"); err != nil {
		return err
	}
	// Readable diffs for key holders. Never enable diff.doctier.cachetextconv:
	// it would cache the decrypted plaintext in git notes inside the repo.
	return gitx.ConfigSet("diff.doctier.textconv", "doctier textconv")
}

func installHooks() error {
	hooks, err := gitx.HooksPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		return err
	}
	for name, body := range map[string]string{
		"pre-commit": preCommitHook,
		"pre-push":   prePushHook,
		"post-merge": postMergeHook,
	} {
		path := filepath.Join(hooks, name)
		if _, err := os.Stat(path); err == nil {
			fmt.Printf("• hook %s already exists — skipping (add 'doctier' calls manually)\n", name)
			continue
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			return err
		}
		fmt.Printf("✓ installed hook %s\n", name)
	}
	return nil
}

// writeIfAbsent writes content to path only when it does not already exist.
func writeIfAbsent(path, content string, perm os.FileMode) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	}
	return true, os.WriteFile(path, []byte(content), perm)
}

// Markers delimit the doctier-managed block in .gitattributes / .gitignore, so
// re-running init after the rules change regenerates it instead of leaving the
// first version frozen (a new private rule that never reaches .gitattributes
// means the filter never runs for it).
const (
	blockBegin = "# doctier:begin — managed by 'doctier init', do not edit"
	blockEnd   = "# doctier:end"
)

// ensureBlock inserts, replaces or removes the managed block in path so it
// holds exactly lines. Everything outside the markers is left untouched.
func ensureBlock(path string, lines []string) error {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	content := string(existing)

	var block string
	if len(lines) > 0 {
		block = blockBegin + "\n" + strings.Join(lines, "\n") + "\n" + blockEnd + "\n"
	}

	var out string
	start := strings.Index(content, blockBegin)
	end := strings.Index(content, blockEnd)
	switch {
	case start >= 0 && end > start:
		rest := strings.TrimPrefix(content[end+len(blockEnd):], "\n")
		out = content[:start] + block + rest
	case block == "":
		return nil // nothing to manage and no stale block to clear
	case strings.TrimSpace(content) == "":
		out = block
	default:
		out = strings.TrimRight(content, "\n") + "\n\n" + block
	}
	if out == content {
		return nil
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return err
	}
	fmt.Printf("✓ updated %s\n", filepath.Base(path))
	return nil
}

func report(created bool, name string) {
	if created {
		fmt.Printf("✓ created %s\n", name)
	} else {
		fmt.Printf("• %s already exists — leaving as is\n", name)
	}
}
