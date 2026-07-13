package cmd

import (
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
	fs := newFlagSet("gc", `usage: doctier gc [--trigger ttl|worktree|pr-merge|branch|all] [--dry-run]

Collect expired ephemeral documents. The default trigger "all" is the safe
local sweep (ttl + worktree); pr-merge and branch collect tracked docs and are
opt-in — run them explicitly from CI or the post-merge hook.`)
	trigger := fs.String("trigger", "all", "ttl|worktree|pr-merge|branch|all")
	dry := fs.Bool("dry-run", false, "show what would be collected without deleting")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// A typo'd trigger (e.g. --trigger pr_merge) must not silently match nothing
	// and print success — a cron/CI job would then never collect, forever.
	switch *trigger {
	case "ttl", "worktree", "pr-merge", "branch", "all":
	default:
		return fmt.Errorf("unknown --trigger %q (want ttl|worktree|pr-merge|branch|all)", *trigger)
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
	var collected, failed int

	if do("ttl") {
		// Sensitive ephemerals are gitignored, so git never lists them; without
		// this the ttl "disk safety net" for local-only files would never fire.
		files = append(files, localOnlyTTLFiles(m, root, files)...)
		n, f := gcTTL(m, root, files, *dry)
		collected, failed = collected+n, failed+f
	}
	if *trigger == "pr-merge" { // opt-in only; never part of "all"
		n, f := gcPRMerge(m, files, *dry)
		collected, failed = collected+n, failed+f
	}
	if *trigger == "branch" { // opt-in only; never part of "all"
		n, f := gcBranch(m, files, *dry)
		collected, failed = collected+n, failed+f
	}
	if do("worktree") {
		gcWorktree(*dry)
	}

	if collected == 0 && failed == 0 {
		fmt.Println("doctier gc: nothing to collect")
	} else if *dry {
		fmt.Printf("doctier gc: %d document(s) would be collected (dry-run)\n", collected)
	} else {
		fmt.Printf("doctier gc: collected %d document(s)\n", collected)
	}
	// A cron/CI gc job must not report success while removals fail forever.
	if failed > 0 && !*dry {
		return fmt.Errorf("%d document(s) could not be collected", failed)
	}
	return nil
}

func gcTTL(m *config.Manifest, root string, files []string, dry bool) (n, failed int) {
	now := time.Now()
	for _, f := range files {
		// Never re-collect quarantined files: a `**`-anchored rule matches them
		// at their trash path too, and each sweep would nest them a level deeper.
		if strings.HasPrefix(f, ".doctier/trash/") {
			continue
		}
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
		if dry {
			n++
			continue
		}
		// Count only removals that actually happened — "collected N" must not
		// include files left in place. A deliberate spare (uncommitted
		// modifications) warns but is not a failure; anything else is.
		removed, err := removeFile(f, abs, root)
		switch {
		case err != nil:
			fmt.Fprintf(os.Stderr, "  ! failed to remove %s: %v\n", f, err)
			failed++
		case !removed:
			fmt.Fprintf(os.Stderr, "  ! spared %s: uncommitted modifications — commit or discard them, then re-run gc\n", f)
		default:
			n++
		}
	}
	return n, failed
}

func gcPRMerge(m *config.Manifest, files []string, dry bool) (n, failed int) {
	return gcOnIntegration(m, files, dry, "pr-merge", func(r config.Rule) bool {
		return r.Expire.On == "pr-merge"
	})
}

// gcBranch collects branch-scoped ephemerals (expire.on=worktree, scope=branch):
// tracked docs that should disappear once their feature branch merges. Collection
// reuses the same integration-branch detection as pr-merge — a merged branch's doc
// is present on the integration branch.
func gcBranch(m *config.Manifest, files []string, dry bool) (n, failed int) {
	return gcOnIntegration(m, files, dry, "branch", config.Rule.BranchScoped)
}

// gcOnIntegration stages deletion of tracked ephemerals (matching want) that have
// reached the integration branch. It only runs there: a doc's branch is considered
// merged once the doc is present on the integration branch. On a feature branch
// these ephemerals are still in flight, so collecting would be destructive (e.g. a
// post-merge hook firing on a routine `git pull`). Shared by pr-merge and branch.
func gcOnIntegration(m *config.Manifest, files []string, dry bool, label string, want func(config.Rule) bool) (n, failed int) {
	integ := m.Ephemeral.IntegrationBranch
	if integ == "" {
		integ = gitx.DefaultBranch()
	}
	cur, err := gitx.CurrentBranch()
	if err != nil || cur != integ {
		fmt.Printf("  %s: skipped — not on integration branch %q (on %q)\n", label, integ, cur)
		return 0, 0
	}

	for _, f := range files {
		rule, ok := m.Match(f)
		if !ok || rule.Lifetime != "ephemeral" || rule.Expire == nil || !want(rule) {
			continue
		}
		// Only tracked docs have "reached the integration branch"; an untracked
		// in-flight file matching the rule has nothing to collect (and git rm
		// would fail on it anyway).
		if !gitx.IsTracked(f) {
			continue
		}
		fmt.Printf("  %s-expired: %s\n", label, f)
		if dry {
			n++
			continue
		}
		if err := gitx.Remove(f); err != nil {
			fmt.Fprintf(os.Stderr, "  ! failed to git rm %s: %v\n", f, err)
			failed++
			continue
		}
		n++
	}
	if n > 0 && !dry {
		fmt.Printf("  → staged deletions; commit them to complete %s collection\n", label)
	}
	return n, failed
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
			// Skip the quarantine dir too: a quarantined file keeps its relative
			// path and mtime, so a `**`-anchored rule would re-match it and every
			// sweep would nest it one level deeper into trash, forever.
			if seen[f] || f == ".git" || strings.HasPrefix(f, ".git/") || strings.HasPrefix(f, ".doctier/trash/") {
				continue
			}
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// removeFile collects f: a staged deletion for tracked files (recoverable from
// git), and a reversible quarantine for untracked (local-only) ones. A tracked
// file with uncommitted modifications is spared (removed=false, no error) — the
// git rm refusal is a protection, not a gc failure — so callers can tell a
// deliberate spare from a removal that actually failed.
func removeFile(f, abs, root string) (removed bool, err error) {
	if gitx.IsTracked(f) {
		if gitx.ModifiedInWorktree(f) {
			return false, nil
		}
		if err := gitx.Remove(f); err != nil {
			return false, err
		}
		return true, nil
	}
	// Untracked file: git has no copy, so an unlink would be unrecoverable. Move
	// it into a quarantine dir instead — reversible, and a second gc sweep (or the
	// user) can purge it later.
	if err := quarantine(abs, root, f); err != nil {
		return false, err
	}
	return true, nil
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
