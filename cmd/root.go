// Package cmd implements the doctier subcommands.
package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rubenglez/doctier/internal/config"
	"github.com/rubenglez/doctier/internal/gitx"
)

const usage = `doctier — tiered privacy and lifecycle for generated docs, over git.

Usage:
  doctier <command> [flags]

Commands:
  init                     Scaffold .doctier.yml, .gitattributes, hooks and filters
  check [--staged]         Verify the staged (or working) tree against the policy (fail-closed)
  status                   Show the effective classification of each document
  agents [--write]         Emit a tier-aware context block for AGENTS.md / CLAUDE.md
  gc [--trigger T]         Collect expired ephemeral docs (ttl|worktree|pr-merge|all)
  grant <ssh-pubkey>       Add a recipient and re-encrypt private docs
  filter clean|smudge <f>  Git clean/smudge filter (invoked by git, not by hand)

Run "doctier <command> -h" for command flags.
`

// Execute is the CLI entry point. It returns a process exit code.
func Execute(args []string) int {
	if len(args) == 0 {
		fmt.Print(usage)
		return 2
	}
	var err error
	switch args[0] {
	case "init":
		err = runInit(args[1:])
	case "check":
		err = runCheck(args[1:])
	case "status":
		err = runStatus(args[1:])
	case "agents":
		err = runAgents(args[1:])
	case "gc":
		err = runGC(args[1:])
	case "grant":
		err = runGrant(args[1:])
	case "filter":
		err = runFilter(args[1:])
	case "-h", "--help", "help":
		fmt.Print(usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "doctier: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "doctier: %v\n", err)
		return 1
	}
	return 0
}

// loadManifest finds the repo root and loads its manifest.
func loadManifest() (*config.Manifest, string, error) {
	root, err := gitx.Root()
	if err != nil {
		return nil, "", fmt.Errorf("not inside a git repository: %w", err)
	}
	m, err := config.Load(filepath.Join(root, config.DefaultPath))
	if err != nil {
		return nil, root, err
	}
	return m, root, nil
}
