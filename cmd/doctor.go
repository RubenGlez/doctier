package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rubenglez/doctier/internal/agex"
	"github.com/rubenglez/doctier/internal/gitx"
)

// runDoctor is a read-only health check of THIS clone against the manifest.
// It sits next to the other two read commands by what it answers:
//
//   - status: how is each document classified?
//   - check:  does the committed content satisfy the policy? (fail-closed gate)
//   - doctor: is the plumbing every private repo depends on actually wired here?
//
// That plumbing — the git filter/diff/merge drivers, the .gitattributes managed
// block, the hooks — lives in .git and does NOT travel with a clone, so a fresh
// clone (or one init'd before a driver was added) can silently be missing it: a
// private doc round-trips as plaintext, or two branches conflict at the
// ciphertext level. doctor also confirms every tracked private blob is intact
// ciphertext, and (when a key is present) that it decrypts.
//
// It exits non-zero if any hard problem is found, so it works as a CI gate. A
// missing key is a warning, not a failure: a keyless CI checkout is legitimate.
func runDoctor(args []string) error {
	fs := newFlagSet("doctor", `usage: doctier doctor

Diagnose this clone's doctier setup: the git filter/diff/merge drivers,
.gitattributes sync with the manifest, hooks, recipients, key availability, and
the integrity of every tracked private file. Read-only. Exits non-zero if any
hard problem is found (warnings, such as a missing key, do not fail).`)
	if err := fs.Parse(args); err != nil {
		return err
	}

	m, root, err := loadManifest()
	if err != nil {
		return err
	}

	encrypts := false
	for _, r := range m.Docs {
		if r.Encrypted() {
			encrypts = true
			break
		}
	}

	d := &doctor{}

	// Encryption plumbing (filters, textconv, merge driver, .gitattributes) only
	// exists to serve private-tracked docs. A public-only repo needs none of it,
	// so reporting it missing would be noise.
	fmt.Println("git config:")
	if encrypts {
		for _, c := range doctierConfig {
			got := gitx.ConfigGet(c.key)
			switch {
			case got == c.value:
				d.ok("%s", c.key)
			case strings.HasSuffix(c.key, ".name"):
				// Cosmetic: git names the driver in merge output but runs it via
				// .driver regardless. Never fail the doctor on it.
				d.warn("%s unset or changed (cosmetic) — run 'doctier init'", c.key)
			case got == "":
				d.fail("%s not set — run 'doctier init'", c.key)
			default:
				d.fail("%s = %q, expected %q — run 'doctier init'", c.key, got, c.value)
			}
		}
	} else {
		d.ok("no private rules — encryption plumbing not required")
	}

	fmt.Println(".gitattributes:")
	if encrypts {
		want := expectedAttrLines(m)
		got, err := managedBlockLines(filepath.Join(root, ".gitattributes"))
		if err != nil {
			d.fail(".gitattributes: %v", err)
		} else {
			gotSet := toSet(got)
			for _, line := range want {
				if gotSet[line] {
					d.ok("%s", line)
				} else {
					// The exact drift the merge driver rollout caused: a private
					// pattern present but missing merge=doctier reads as a missing
					// expected line here.
					d.fail("missing or stale: %q — run 'doctier init'", line)
				}
			}
			wantSet := toSet(want)
			for _, line := range got {
				if !wantSet[line] {
					d.warn("unexpected managed line: %q (a rule removed? run 'doctier init')", line)
				}
			}
		}
	} else {
		d.ok("no private rules — no managed attributes required")
	}

	fmt.Println("hooks:")
	if hooksDir, err := gitx.HooksPath(); err != nil {
		d.fail("cannot resolve hooks path: %v", err)
	} else {
		if !filepath.IsAbs(hooksDir) {
			hooksDir = filepath.Join(root, hooksDir)
		}
		for _, name := range []string{"pre-commit", "pre-push", "post-merge"} {
			body, err := os.ReadFile(filepath.Join(hooksDir, name))
			switch {
			case err != nil:
				d.fail("hook %s missing — run 'doctier init'", name)
			case !strings.Contains(string(body), "doctier"):
				d.warn("hook %s exists but does not call doctier (custom hook?)", name)
			default:
				d.ok("hook %s", name)
			}
		}
	}

	// Everything below is about encrypted content; a public-only repo is done.
	if !encrypts {
		return d.summary()
	}

	fmt.Println("recipients:")
	recPath := recipientsPath(m, root)
	if _, err := agex.LoadRecipients(recPath); err != nil {
		d.fail("recipients file %s unusable: %v", m.RecipientsFile, err)
	} else {
		lines, _ := agex.RecipientLines(recPath)
		d.ok("%d recipient(s) in %s", len(lines), m.RecipientsFile)
	}

	fmt.Println("key:")
	id, keyErr := agex.LoadIdentity("")
	if keyErr != nil {
		d.warn("no usable key here — private docs stay ciphertext (fine for a keyless CI checkout): %v", keyErr)
	} else {
		d.ok("decryption key available")
	}

	// Private-file integrity. The index blob is what git publishes; a plaintext
	// blob at a private path is a leak even when the working tree looks
	// encrypted. When a key is present, also confirm each blob actually decrypts.
	fmt.Println("private files:")
	tracked, err := gitx.TrackedFiles()
	if err != nil {
		d.fail("cannot list tracked files: %v", err)
		return d.summary()
	}
	var private []string
	for _, f := range tracked {
		if rule, ok := m.Match(f); ok && rule.Encrypted() {
			private = append(private, f)
		}
	}
	blobs, err := gitx.StagedBlobs(private)
	if err != nil {
		d.fail("cannot read index blobs: %v", err)
		return d.summary()
	}
	intact := 0
	for _, f := range private {
		blob, ok := blobs[f]
		if !ok {
			continue // tracked but no index blob (mid-operation); nothing to verify
		}
		if !agex.ValidCiphertext(blob) {
			d.fail("%s: not valid age ciphertext in the index (plaintext leak or corruption)", f)
			continue
		}
		if keyErr == nil {
			if _, err := agex.Decrypt(blob, id); err != nil {
				d.fail("%s: does not decrypt with your key (encrypted to a stale recipient set?): %v", f, err)
				continue
			}
		}
		intact++
	}
	if intact > 0 {
		note := "valid ciphertext"
		if keyErr == nil {
			note = "decrypt cleanly"
		}
		d.ok("%d/%d tracked private file(s) %s", intact, len(private), note)
	} else if len(private) == 0 {
		d.ok("no tracked private files yet")
	}

	return d.summary()
}

// doctor accumulates a health-check report and its failure/warning counts.
type doctor struct {
	fails, warns int
}

func (d *doctor) ok(format string, a ...any) { fmt.Printf("  ✓ %s\n", fmt.Sprintf(format, a...)) }
func (d *doctor) warn(format string, a ...any) {
	d.warns++
	fmt.Printf("  ⚠ %s\n", fmt.Sprintf(format, a...))
}
func (d *doctor) fail(format string, a ...any) {
	d.fails++
	fmt.Printf("  ✗ %s\n", fmt.Sprintf(format, a...))
}

// summary prints the tally and returns a non-zero-exit error when anything hard
// failed, so `doctier doctor` is usable as a CI gate.
func (d *doctor) summary() error {
	fmt.Println()
	if d.fails == 0 {
		if d.warns == 0 {
			fmt.Println("✓ doctier: setup healthy")
		} else {
			fmt.Printf("✓ doctier: setup healthy (%d warning(s))\n", d.warns)
		}
		return nil
	}
	return fmt.Errorf("%d problem(s), %d warning(s) — see above", d.fails, d.warns)
}

// managedBlockLines returns the non-empty lines inside the doctier-managed block
// of a .gitattributes/.gitignore file, or nil when the file or block is absent.
func managedBlockLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	content := string(data)
	start := strings.Index(content, blockBegin)
	if start < 0 {
		return nil, nil
	}
	tail := content[start+len(blockBegin):]
	end := strings.Index(tail, blockEnd)
	if end < 0 {
		end = len(tail) // corrupt block with no end marker: treat the rest as the block
	}
	var lines []string
	for _, ln := range strings.Split(tail[:end], "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			lines = append(lines, ln)
		}
	}
	return lines, nil
}

func toSet(lines []string) map[string]bool {
	s := make(map[string]bool, len(lines))
	for _, l := range lines {
		s[l] = true
	}
	return s
}
