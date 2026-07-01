// Package gitx wraps the small set of git commands doctier shells out to.
package gitx

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
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

// StagedFiles lists paths staged for commit (added/copied/modified/renamed).
// R matters: with rename detection on, a plaintext file moved into a private
// path shows up as a rename, and skipping it would let it past the pre-commit
// check.
func StagedFiles() ([]string, error) {
	out, err := run("diff", "--cached", "--name-only", "--diff-filter=ACMR")
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

// StagedBlobs returns the index content of each path that is present in the
// index, in a single cat-file process (StagedBlob spawns one per call, which
// crawls on large trees). Paths not in the index are simply absent from the map.
func StagedBlobs(paths []string) (map[string][]byte, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	cmd := exec.Command("git", "cat-file", "--batch")
	var in bytes.Buffer
	for _, p := range paths {
		in.WriteString(":" + p + "\n")
	}
	cmd.Stdin = &in
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git cat-file --batch: %v: %s", err, strings.TrimSpace(errb.String()))
	}

	blobs := make(map[string][]byte, len(paths))
	r := bufio.NewReader(&out)
	for _, p := range paths {
		header, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("cat-file --batch: short output: %w", err)
		}
		// "<object> missing" (not in the index) or "<oid> <type> <size>". The
		// object name echoes the input path and may contain spaces, so detect
		// the missing case by suffix, not by field count.
		if strings.HasSuffix(strings.TrimSpace(header), " missing") {
			continue
		}
		fields := strings.Fields(header)
		if len(fields) != 3 {
			return nil, fmt.Errorf("cat-file --batch: unexpected header %q", strings.TrimSpace(header))
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("cat-file --batch: bad size in %q", strings.TrimSpace(header))
		}
		content := make([]byte, size)
		if _, err := io.ReadFull(r, content); err != nil {
			return nil, fmt.Errorf("cat-file --batch: truncated content for %s: %w", p, err)
		}
		if _, err := r.ReadString('\n'); err != nil { // trailing LF after content
			return nil, fmt.Errorf("cat-file --batch: missing terminator for %s: %w", p, err)
		}
		blobs[p] = content
	}
	return blobs, nil
}

// IsTracked reports whether path is in the index.
func IsTracked(path string) bool {
	out, err := run("ls-files", "--", path)
	return err == nil && out != ""
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

// DefaultBranch returns the integration branch pr-merge ephemerals are collected
// on: origin's HEAD if known, else a local main/master, else "main".
func DefaultBranch() string {
	if out, err := run("symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil && out != "" {
		return strings.TrimPrefix(out, "origin/")
	}
	for _, b := range []string{"main", "master"} {
		if _, err := run("rev-parse", "--verify", "--quiet", "refs/heads/"+b); err == nil {
			return b
		}
	}
	return "main"
}

// LastCommitTime returns the committer date of the most recent commit that
// touched path. It errors when no commit touches path (untracked/uncommitted),
// so callers can fall back to filesystem mtime. Unlike mtime, this survives
// clones and checkouts.
func LastCommitTime(path string) (time.Time, error) {
	out, err := run("log", "-1", "--format=%ct", "--", path)
	if err != nil {
		return time.Time{}, err
	}
	if out == "" {
		return time.Time{}, fmt.Errorf("no commits for %s", path)
	}
	sec, err := strconv.ParseInt(out, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(sec, 0), nil
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

// ConfigGet returns a local git config value, or "" when unset.
func ConfigGet(key string) string {
	out, err := run("config", "--local", "--get", key)
	if err != nil {
		return ""
	}
	return out
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
