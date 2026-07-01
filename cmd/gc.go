package cmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rubenglez/doctier/internal/config"
	"github.com/rubenglez/doctier/internal/gitx"
)

// runGC collects expired ephemeral documents. Triggers:
//
//	ttl       delete files past their ttl_days (by mtime)
//	pr-merge  stage deletion of tracked pr-merge ephemerals (for post-merge/CI)
//	worktree  prune bookkeeping for removed worktrees
//	all       ttl + worktree (the safe local sweep; pr-merge is opt-in)
func runGC(args []string) error {
	fs := flag.NewFlagSet("gc", flag.ContinueOnError)
	trigger := fs.String("trigger", "all", "ttl|worktree|pr-merge|all")
	dry := fs.Bool("dry-run", false, "show what would be collected without deleting")
	if err := fs.Parse(args); err != nil {
		return err
	}

	m, root, err := loadManifest()
	if err != nil {
		return err
	}
	files, err := gitx.ListFiles()
	if err != nil {
		return err
	}

	do := func(t string) bool { return *trigger == t || *trigger == "all" }
	var collected int

	if do("ttl") {
		collected += gcTTL(m, root, files, *dry)
	}
	if *trigger == "pr-merge" { // opt-in only; never part of "all"
		collected += gcPRMerge(m, files, *dry)
	}
	if do("worktree") {
		gcWorktree(*dry)
	}

	if collected == 0 {
		fmt.Println("doctier gc: nothing to collect")
	} else if *dry {
		fmt.Printf("doctier gc: %d document(s) would be collected (dry-run)\n", collected)
	} else {
		fmt.Printf("doctier gc: collected %d document(s)\n", collected)
	}
	return nil
}

func gcTTL(m *config.Manifest, root string, files []string, dry bool) int {
	now := time.Now()
	n := 0
	for _, f := range files {
		rule, ok := m.Match(f)
		if !ok || rule.Lifetime != "ephemeral" || rule.Expire == nil || rule.Expire.On != "ttl" {
			continue
		}
		abs := filepath.Join(root, f)
		info, err := os.Stat(abs)
		if err != nil {
			continue
		}
		age := now.Sub(info.ModTime())
		if age < time.Duration(rule.Expire.TTLDays)*24*time.Hour {
			continue
		}
		fmt.Printf("  ttl-expired: %s (%dd old)\n", f, int(age.Hours()/24))
		n++
		if dry {
			continue
		}
		if err := removeFile(f, abs); err != nil {
			fmt.Fprintf(os.Stderr, "  ! failed to remove %s: %v\n", f, err)
		}
	}
	return n
}

func gcPRMerge(m *config.Manifest, files []string, dry bool) int {
	n := 0
	for _, f := range files {
		rule, ok := m.Match(f)
		if !ok || rule.Lifetime != "ephemeral" || rule.Expire == nil || rule.Expire.On != "pr-merge" {
			continue
		}
		fmt.Printf("  pr-merge-expired: %s\n", f)
		n++
		if dry {
			continue
		}
		if err := gitx.Remove(f); err != nil {
			fmt.Fprintf(os.Stderr, "  ! failed to git rm %s: %v\n", f, err)
		}
	}
	if n > 0 && !dry {
		fmt.Println("  → staged deletions; commit them to complete pr-merge collection")
	}
	return n
}

func gcWorktree(dry bool) {
	if dry {
		fmt.Println("  worktree: would run 'git worktree prune'")
		return
	}
	// Worktree-scoped ephemerals live inside each worktree and are removed with
	// it; here we only prune stale worktree bookkeeping.
	if _, err := gitx.Worktrees(); err == nil {
		_ = gitx.Prune()
	}
}

// removeFile deletes f, using git rm when tracked so the deletion is staged.
func removeFile(f, abs string) error {
	if err := gitx.Remove(f); err == nil {
		return nil
	}
	return os.Remove(abs)
}
