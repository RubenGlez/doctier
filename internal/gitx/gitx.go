// Package gitx wraps the small set of git commands doctier shells out to.
package gitx

import (
	"bytes"
	"os/exec"
	"strings"
)

// run executes git with args and returns stdout, trimmed. stderr is folded into
// the error so callers get a useful message.
func run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg != "" {
			return "", &Error{Args: args, Msg: msg, Err: err}
		}
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

// Error carries the failing git invocation for readable diagnostics.
type Error struct {
	Args []string
	Msg  string
	Err  error
}

func (e *Error) Error() string {
	return "git " + strings.Join(e.Args, " ") + ": " + e.Msg
}

// Root returns the top-level directory of the current worktree.
func Root() (string, error) { return run("rev-parse", "--show-toplevel") }

// StagedFiles lists paths staged for commit (added/copied/modified).
func StagedFiles() ([]string, error) {
	out, err := run("diff", "--cached", "--name-only", "--diff-filter=ACM")
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

// ListFiles lists tracked and untracked (non-ignored) files.
func ListFiles() ([]string, error) {
	out, err := run("ls-files", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

// TrackedFiles lists tracked files only.
func TrackedFiles() ([]string, error) {
	out, err := run("ls-files")
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

// StagedBlob returns the staged (index) content of path, or an error if the
// path is not in the index.
func StagedBlob(path string) ([]byte, error) {
	cmd := exec.Command("git", "cat-file", "blob", ":"+path)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Remove stages the deletion of path from the working tree and index.
func Remove(path string) error {
	_, err := run("rm", "-q", "--", path)
	return err
}

// Worktrees returns the absolute paths of all linked worktrees plus the main one.
func Worktrees() ([]string, error) {
	out, err := run("worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range splitLines(out) {
		if strings.HasPrefix(line, "worktree ") {
			paths = append(paths, strings.TrimPrefix(line, "worktree "))
		}
	}
	return paths, nil
}

// Prune removes bookkeeping for worktrees whose directories are gone.
func Prune() error {
	_, err := run("worktree", "prune")
	return err
}

// CurrentBranch returns the checked-out branch name, or "" if detached.
func CurrentBranch() (string, error) {
	out, err := run("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	if out == "HEAD" {
		return "", nil
	}
	return out, nil
}

// ConfigSet sets a local git config key.
func ConfigSet(key, value string) error {
	_, err := run("config", "--local", key, value)
	return err
}

// HooksPath returns the effective hooks directory.
func HooksPath() (string, error) {
	if p, err := run("config", "--local", "core.hooksPath"); err == nil && p != "" {
		return p, nil
	}
	gitDir, err := run("rev-parse", "--git-path", "hooks")
	if err != nil {
		return "", err
	}
	return gitDir, nil
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
