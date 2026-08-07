package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/rubenglez/doctier/internal/agex"
)

// runMerge is the git merge driver for private (encrypted) docs, invoked as
// `doctier merge %O %A %B %P` (ancestor, current, other, real path). Age
// ciphertext is randomized, so ANY two branches touching the same private doc
// conflict at the armor level and git's text merge would interleave base64.
// Instead: decrypt the three sides and 3-way merge the plaintext.
//
// Git treats the driver's %A output as REPOSITORY-format content: on a clean
// merge it becomes the merged blob directly (no clean filter runs, and no
// pre-commit hook guards a merge commit), so the result of a private path
// must be re-encrypted before it is handed back — writing plaintext would
// store a cleartext blob in the merge commit. On a conflict git stages the
// three original blobs and only the worktree gets the output. Linux and macOS
// receive plaintext conflict markers directly. Windows receives encrypted
// markers and materializes them safely through `doctier unlock`; after the user
// resolves them, the clean filter re-encrypts on git add.
//
// Without a usable key the driver cannot merge; it leaves the current side in
// place and exits non-zero (a conflict for git) with instructions.
func runMerge(args []string) error {
	if len(args) < 4 {
		return fmt.Errorf("merge: usage: doctier merge <base> <current> <other> <path> (invoked by git, not by hand)")
	}
	base, current, other, path := args[0], args[1], args[2], args[3]

	m, root, err := loadManifest()
	if err != nil {
		return err
	}

	sides := [3]string{base, current, other}
	var contents [3][]byte
	needsKey := false
	for i, f := range sides {
		data, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("merge: read %s: %w", f, err)
		}
		contents[i] = data
		if agex.IsEncrypted(data) {
			needsKey = true
		}
	}

	rule, matched := m.Match(path)
	private := matched && rule.Encrypted()

	// Non-private path with no encrypted side: plain 3-way text merge.
	if !private && !needsKey {
		merged, conflicted, err := merge3(contents)
		if err != nil {
			return err
		}
		if err := os.WriteFile(current, merged, 0o600); err != nil {
			return err
		}
		if conflicted {
			return conflictErr(path)
		}
		return nil
	}

	if needsKey {
		id, err := agex.LoadIdentity("")
		if err != nil {
			return fmt.Errorf("merge: %s is encrypted and no key is available to merge it: %w\n  keep one side instead: git checkout --ours -- %q  (or --theirs), then git add", path, err, path)
		}
		for i := range contents {
			if !agex.IsEncrypted(contents[i]) {
				continue // e.g. an empty base on an add/add conflict
			}
			pt, err := agex.Decrypt(contents[i], id)
			if err != nil {
				return fmt.Errorf("merge: decrypt %s side of %s: %w", [3]string{"base", "ours", "theirs"}[i], path, err)
			}
			contents[i] = pt
		}
	}

	merged, conflicted, err := merge3(contents)
	if err != nil {
		return err
	}
	if conflicted {
		if private && runtime.GOOS == "windows" {
			// Git for Windows copies the merge driver's temporary %A output
			// into the worktree after the driver exits. That copy inherits the
			// directory DACL, so returning plaintext here would bypass the
			// owner-only boundary. Keep the conflict encrypted until unlock can
			// protect the real worktree path before writing plaintext to it.
			recipients, err := agex.LoadRecipients(recipientsPath(m, root))
			if err != nil {
				return fmt.Errorf("merge: cannot encrypt the conflicted %s: %w", path, err)
			}
			ct, err := agex.Encrypt(merged, recipients)
			if err != nil {
				return fmt.Errorf("merge: encrypt conflicted %s: %w", path, err)
			}
			if err := os.WriteFile(current, ct, 0o600); err != nil {
				return err
			}
			return fmt.Errorf("merge: conflicts in %s: run `doctier unlock` to materialize protected plaintext conflict markers, resolve them, then git add", path)
		}
		// Only the worktree receives this; the index keeps the three encrypted
		// stages, so plaintext markers here leak nothing into git. The merge
		// result is still sensitive plaintext on disk, so protect %A before
		// writing it; Git uses that file as the worktree result.
		if err := writeOwnerOnly(current, merged); err != nil {
			return err
		}
		return conflictErr(path)
	}
	if private {
		recipients, err := agex.LoadRecipients(recipientsPath(m, root))
		if err != nil {
			return fmt.Errorf("merge: cannot re-encrypt the merged %s: %w", path, err)
		}
		ct, err := agex.Encrypt(merged, recipients)
		if err != nil {
			return fmt.Errorf("merge: re-encrypt %s: %w", path, err)
		}
		merged = ct
	}
	return os.WriteFile(current, merged, 0o600)
}

func conflictErr(path string) error {
	return fmt.Errorf("merge: conflicts in %s — plaintext conflict markers are in the file; resolve them, then git add (the clean filter re-encrypts)", path)
}

// merge3 3-way merges contents (base, current, other) via git merge-file and
// returns the merged bytes plus whether conflict markers were left.
func merge3(contents [3][]byte) (merged []byte, conflicted bool, err error) {
	tmp, err := os.MkdirTemp("", "doctier-merge")
	if err != nil {
		return nil, false, err
	}
	defer os.RemoveAll(tmp)

	files := [3]string{filepath.Join(tmp, "base"), filepath.Join(tmp, "ours"), filepath.Join(tmp, "theirs")}
	for i, f := range files {
		// Encrypted sides have already been decrypted before merge3. Apply the
		// platform security boundary before any plaintext reaches these files.
		if err := writeOwnerOnly(f, contents[i]); err != nil {
			return nil, false, err
		}
	}
	// merge-file merges INTO its first argument; non-zero exit = conflicts.
	cmd := exec.Command("git", "merge-file", "-L", "ours", "-L", "base", "-L", "theirs",
		files[1], files[0], files[2])
	cmd.Stderr = os.Stderr
	mergeErr := cmd.Run()

	merged, err = os.ReadFile(files[1])
	if err != nil {
		return nil, false, err
	}
	return merged, mergeErr != nil, nil
}
