package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rubenglez/doctier/internal/agex"
	"github.com/rubenglez/doctier/internal/gitx"
)

// runUnlock decrypts every private-tracked file into the working tree using the
// available identity. It is the missing piece for two personas:
//
//   - a fresh clone on a keyed machine, where nothing re-runs the smudge filter
//     so `git checkout`/`reset` leave ciphertext in place; and
//   - a headless/CI/agent run, where the key arrives via $DOCTIER_IDENTITY and
//     there is no interactive checkout at all.
//
// It reads each file's ciphertext from the index (which always holds the
// encrypted blob) and writes plaintext to disk.
func runUnlock(args []string) error {
	fs := newFlagSet("unlock", `usage: doctier unlock

Decrypt every private tracked file from the index into the working tree. Use it
after cloning (nothing re-runs the smudge filter on an already-checked-out
tree) or in a headless run with $DOCTIER_SSH_KEY set to a path or inline PEM
(granting a headless environment access: docs/agents.md). Files already
plaintext in the working tree are left untouched.`)
	if err := fs.Parse(args); err != nil {
		return err
	}
	m, root, err := loadManifest()
	if err != nil {
		return err
	}
	id, err := agex.LoadIdentity("")
	if err != nil {
		// The headless/agent case deserves the full pointer: a key generated
		// here cannot self-serve (it can't decrypt blobs encrypted before it
		// was a recipient), which is easy to burn time discovering.
		return fmt.Errorf("unlock: no usable key: %w\n  access is granted by an existing recipient running `doctier grant \"<your pubkey>\"` — a key added here cannot decrypt existing docs; see docs/agents.md", err)
	}
	files, err := gitx.TrackedFiles()
	if err != nil {
		return err
	}
	n, failed, kept := 0, 0, 0
	for _, f := range files {
		rule, ok := m.Match(f)
		if !ok || !rule.Encrypted() {
			continue
		}
		dest := filepath.Join(root, f)
		// Never clobber a worktree copy that is already plaintext: it may hold
		// uncommitted edits (an earlier unlock or a smudged checkout, then work).
		// Overwriting it with the index version would silently destroy that work.
		if cur, err := os.ReadFile(dest); err == nil && !agex.IsEncrypted(cur) {
			kept++
			continue
		}
		blob, err := gitx.StagedBlob(f)
		if err != nil {
			continue
		}
		if !agex.IsEncrypted(blob) {
			continue // already plaintext in the index (added before the filter)
		}
		pt, err := agex.Decrypt(blob, id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ! %s: %v\n", f, err)
			failed++
			continue
		}
		// 0600: this is deliberately-encrypted content landing on disk. Tighten
		// before writing — WriteFile's perm only applies when creating, and the
		// ciphertext copy usually already exists (wider) at dest.
		_ = os.Chmod(dest, 0o600)
		if err := os.WriteFile(dest, pt, 0o600); err != nil {
			return err
		}
		n++
	}
	fmt.Printf("✓ doctier: decrypted %d file(s) into the working tree\n", n)
	if kept > 0 {
		fmt.Printf("  (%d file(s) already plaintext in the working tree — left untouched)\n", kept)
	}
	if failed > 0 {
		return fmt.Errorf("%d file(s) could not be decrypted with this key — it is not among their recipients; an existing recipient must run `doctier grant \"<this key.pub>\"` and commit, then pull here (see docs/agents.md)", failed)
	}
	return nil
}

// runCat writes the plaintext of a single private file to stdout without
// materializing it on disk — the read-only path for an agent that only needs to
// read one doc. It prefers the index blob (canonical ciphertext) and falls back
// to the working-tree file.
func runCat(args []string) error {
	fs := newFlagSet("cat", `usage: doctier cat <path>

Print one private file's plaintext to stdout without writing it to disk — the
read-only path for an agent or script that only needs to read a doc.`)
	if err := fs.Parse(args); err != nil {
		return err
	}
	args = fs.Args()
	if len(args) < 1 {
		return fmt.Errorf("cat: usage: doctier cat <path>")
	}
	_, root, err := loadManifest()
	if err != nil {
		return err
	}
	rel := args[0]
	blob, err := gitx.StagedBlob(rel)
	if err != nil {
		blob, err = os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return fmt.Errorf("cat: %s: not in the index or working tree", rel)
		}
	}
	if !agex.IsEncrypted(blob) {
		_, err = os.Stdout.Write(blob)
		return err
	}
	id, err := agex.LoadIdentity("")
	if err != nil {
		return fmt.Errorf("cat: no usable key: %w\n  access is granted by an existing recipient running `doctier grant \"<your pubkey>\"` — a key added here cannot decrypt existing docs; see docs/agents.md", err)
	}
	pt, err := agex.Decrypt(blob, id)
	if err != nil {
		return fmt.Errorf("cat: decrypt %s: %w — this key is not among the doc's recipients; an existing recipient must grant it (see docs/agents.md)", rel, err)
	}
	_, err = os.Stdout.Write(pt)
	return err
}
