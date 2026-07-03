package cmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/rubenglez/doctier/internal/config"
	"github.com/rubenglez/doctier/internal/gitx"
)

// runGC collects expired ephemeral documents. Triggers:
//
//	ttl       delete files past their ttl_days (by mtime)
//	pr-merge  stage deletion of tracked pr-merge ephemerals (for post-merge/CI)
//	branch    stage deletion of tracked branch-scoped ephemerals whose branch merged
//	worktree  prune bookkeeping for removed worktrees
//	all       ttl + worktree (the safe local sweep; pr-merge and branch are opt-in)
func runGC(args []string) error {
	fs := flag.NewFlagSet("gc", flag.ContinueOnError)
	trigger := fs.String("trigger", "all", "ttl|worktree|pr-merge|branch|all")
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
		// Sensitive ephemerals are gitignored, so git never lists them; without
		// this the ttl "disk safety net" for local-only files would never fire.
		files = append(files, localOnlyTTLFiles(m, root, files)...)
		collected += gcTTL(m, root, files, *dry)
	}
	if *trigger == "pr-merge" { // opt-in only; never part of "all"
		collected += gcPRMerge(m, files, *dry)
	}
	if *trigger == "branch" { // opt-in only; never part of "all"
		collected += gcBranch(m, files, *dry)
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
		// Prefer the last commit date (survives clones/checkouts). Fall back to
		// filesystem mtime ONLY for local-only (sensitive, never-committed) files —
		// those legitimately have no commit history. For a tracked-rule file with
		// no commit history (e.g. a fresh `cp -p`/`rsync -a` copy that carries an
		// old mtime), deleting by mtime would destroy data git never had, so skip.
		ref, err := gitx.LastCommitTime(f)
		if err != nil {
			if !rule.LocalOnly() {
				continue
			}
			info, statErr := os.Stat(abs)
			if statErr != nil {
				continue
			}
			ref = info.ModTime()
		}
		age := now.Sub(ref)
		if age < time.Duration(rule.Expire.TTLDays)*24*time.Hour {
			continue
		}
		fmt.Printf("  ttl-expired: %s (%dd old)\n", f, int(age.Hours()/24))
		n++
		if dry {
			continue
		}
		if err := removeFile(f, abs, root); err != nil {
			fmt.Fprintf(os.Stderr, "  ! failed to remove %s: %v\n", f, err)
		}
	}
	return n
}

func gcPRMerge(m *config.Manifest, files []string, dry bool) int {
	return gcOnIntegration(m, files, dry, "pr-merge", func(r config.Rule) bool {
		return r.Expire.On == "pr-merge"
	})
}

// gcBranch collects branch-scoped ephemerals (expire.on=worktree, scope=branch):
// tracked docs that should disappear once their feature branch merges. Collection
// reuses the same integration-branch detection as pr-merge — a merged branch's doc
// is present on the integration branch.
func gcBranch(m *config.Manifest, files []string, dry bool) int {
	return gcOnIntegration(m, files, dry, "branch", config.Rule.BranchScoped)
}

// gcOnIntegration stages deletion of tracked ephemerals (matching want) that have
// reached the integration branch. It only runs there: a doc's branch is considered
// merged once the doc is present on the integration branch. On a feature branch
// these ephemerals are still in flight, so collecting would be destructive (e.g. a
// post-merge hook firing on a routine `git pull`). Shared by pr-merge and branch.
func gcOnIntegration(m *config.Manifest, files []string, dry bool, label string, want func(config.Rule) bool) int {
	integ := m.Ephemeral.IntegrationBranch
	if integ == "" {
		integ = gitx.DefaultBranch()
	}
	cur, err := gitx.CurrentBranch()
	if err != nil || cur != integ {
		fmt.Printf("  %s: skipped — not on integration branch %q (on %q)\n", label, integ, cur)
		return 0
	}

	n := 0
	for _, f := range files {
		rule, ok := m.Match(f)
		if !ok || rule.Lifetime != "ephemeral" || rule.Expire == nil || !want(rule) {
			continue
		}
		fmt.Printf("  %s-expired: %s\n", label, f)
		n++
		if dry {
			continue
		}
		if err := gitx.Remove(f); err != nil {
			fmt.Fprintf(os.Stderr, "  ! failed to git rm %s: %v\n", f, err)
		}
	}
	if n > 0 && !dry {
		fmt.Printf("  → staged deletions; commit them to complete %s collection\n", label)
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

// localOnlyTTLFiles globs the worktree for files covered by local-only ttl
// rules, skipping .git and anything git already listed.
func localOnlyTTLFiles(m *config.Manifest, root string, listed []string) []string {
	seen := make(map[string]bool, len(listed))
	for _, f := range listed {
		seen[f] = true
	}
	fsys := os.DirFS(root)
	var out []string
	for _, r := range m.Docs {
		if !r.LocalOnly() || r.Expire == nil || r.Expire.On != "ttl" {
			continue
		}
		matches, _ := doublestar.Glob(fsys, r.Path, doublestar.WithFilesOnly())
		for _, f := range matches {
			if seen[f] || f == ".git" || strings.HasPrefix(f, ".git/") {
				continue
			}
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// removeFile collects f: a staged deletion for tracked files (recoverable from
// git), and a reversible quarantine for untracked (local-only) ones. When git rm
// refuses a tracked file — e.g. it has uncommitted modifications — that refusal
// is a protection, so propagate it instead of deleting the changes unrecoverably.
func removeFile(f, abs, root string) error {
	if gitx.IsTracked(f) {
		return gitx.Remove(f)
	}
	// Untracked file: git has no copy, so an unlink would be unrecoverable. Move
	// it into a quarantine dir instead — reversible, and a second gc sweep (or the
	// user) can purge it later.
	return quarantine(abs, root, f)
}

// quarantine moves an untracked file into .doctier/trash/, preserving its
// relative path, instead of unlinking it. The dir is gitignored by init.
func quarantine(abs, root, rel string) error {
	dest := filepath.Join(root, ".doctier", "trash", rel)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := os.Rename(abs, dest); err != nil {
		// Cross-device or other rename failure: fall back to copy+remove so the
		// file still leaves its ttl-covered location, but never lose it silently.
		data, readErr := os.ReadFile(abs)
		if readErr != nil {
			return readErr
		}
		if writeErr := os.WriteFile(dest, data, 0o600); writeErr != nil {
			return writeErr
		}
		return os.Remove(abs)
	}
	return nil
}
