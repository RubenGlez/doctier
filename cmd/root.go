// Package cmd implements the doctier subcommands.
package cmd

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rubenglez/doctier/internal/config"
	"github.com/rubenglez/doctier/internal/gitx"
)

// Version is the CLI version, overridden at build time via -ldflags by the
// release pipeline. It stays "dev" for local `go build` / `go install` builds.
var Version = "dev"

const usage = `doctier — tiered privacy and lifecycle for generated docs, over git.

Usage:
  doctier <command> [flags]

Commands:
  init                     Scaffold .doctier.yml, .gitattributes, hooks and filters
  check [--staged|--push]  Verify staged / working tree / pushed commits against the policy (fail-closed)
  status                   Show the effective classification of each document
  agents [--write]         Emit a tier-aware context block for AGENTS.md / CLAUDE.md
  gc [--trigger T]         Collect expired ephemeral docs (ttl|worktree|pr-merge|branch|all)
  grant [<ssh-pubkey>]     Add a recipient and re-encrypt private docs; with no
                           key, just re-encrypt to the current set (revoke flow)
  unlock                   Decrypt all private files into the working tree (needs a key)
  cat <path>               Print one private file's plaintext to stdout (needs a key)
  filter clean|smudge <f>  Git clean/smudge filter (invoked by git, not by hand)
  textconv <f>             Git diff textconv driver (invoked by git, not by hand)
  version                  Print the doctier version

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
	case "unlock":
		err = runUnlock(args[1:])
	case "cat":
		err = runCat(args[1:])
	case "filter":
		err = runFilter(args[1:])
	case "textconv":
		err = runTextconv(args[1:])
	case "version", "--version", "-v":
		fmt.Printf("doctier %s\n", Version)
		return 0
	case "-h", "--help", "help":
		fmt.Print(usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "doctier: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	if err != nil {
		// "doctier <cmd> -h" is a help request, not a failure: the FlagSet has
		// already printed usage, so exit 0 without a leaked parse error.
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintf(os.Stderr, "doctier: %v\n", err)
		return 1
	}
	return 0
}

// newFlagSet returns a FlagSet for a subcommand whose -h/--help prints the
// given usage line plus any registered flags. Every subcommand must parse its
// args through one of these — a command that ignores args would otherwise
// RUN on "doctier <cmd> -h" instead of printing help.
func newFlagSet(name, usage string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), usage)
		fs.PrintDefaults()
	}
	return fs
}

// loadManifest finds the repo root and loads its manifest.
func loadManifest() (*config.Manifest, string, error) {
	root, err := gitx.Root()
	if err != nil {
		return nil, "", fmt.Errorf("not inside a git repository: %w", err)
	}
	m, err := config.Load(filepath.Join(root, config.DefaultPath))
	if err != nil {
		// The most common first error: a command run before init. A raw ENOENT
		// tells the user nothing about what to do next.
		if errors.Is(err, os.ErrNotExist) {
			return nil, root, fmt.Errorf("no .doctier.yml found — run 'doctier init' to set up this repository")
		}
		return nil, root, err
	}
	return m, root, nil
}
