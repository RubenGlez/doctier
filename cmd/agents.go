package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rubenglez/doctier/internal/agex"
	"github.com/rubenglez/doctier/internal/config"
	"github.com/rubenglez/doctier/internal/gitx"
)

// Markers delimit the doctier-managed region inside the target file so the block
// can be replaced in place on every run.
const (
	agentsBegin = "<!-- doctier:begin -->"
	agentsEnd   = "<!-- doctier:end -->"
)

// runAgents emits a tier-aware pointer block for AGENTS.md / CLAUDE.md: the
// discovery convention agents already auto-load. It lists only the documents the
// user explicitly classified (so it skips source code, which is uncovered) and
// only those that are readable now (a private doc left as ciphertext for want of
// a key is skipped). doctier does not expose tiers to the agent; it curates the
// index that the convention surfaces.
func runAgents(args []string) error {
	fs := newFlagSet("agents", `usage: doctier agents [--write] [--file AGENTS.md]

Emit a tier-aware context block listing the classified, currently-readable
docs — print it, or --write to maintain a managed block in the target file.`)
	write := fs.Bool("write", false, "insert/update the managed block in the target file instead of printing it")
	file := fs.String("file", "AGENTS.md", "target file for --write")
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

	durable, ephemeral := classifiedDocs(m, root, files)
	block := renderBlock(durable, ephemeral)
	if *write {
		return writeBlock(root, *file, block)
	}
	fmt.Print(block)
	return nil
}

// classifiedDocs partitions the explicitly-classified, currently-readable docs
// into durable and ephemeral, sorted.
func classifiedDocs(m *config.Manifest, root string, files []string) (durable, ephemeral []string) {
	sort.Strings(files)
	for _, f := range files {
		rule, ok := m.Match(f)
		if !ok {
			continue // uncovered (source code, incidental files) — not context
		}
		if rule.Encrypted() && !readable(root, f) {
			continue // private but not decrypted here (no key): pointing at ciphertext is noise
		}
		if rule.Lifetime == "ephemeral" {
			ephemeral = append(ephemeral, f)
		} else {
			durable = append(durable, f)
		}
	}
	return durable, ephemeral
}

// readable reports whether f exists and is not age ciphertext.
func readable(root, f string) bool {
	data, err := os.ReadFile(filepath.Join(root, f))
	if err != nil {
		return false
	}
	return !agex.IsEncrypted(data)
}

func renderBlock(durable, ephemeral []string) string {
	var b strings.Builder
	b.WriteString(agentsBegin + "\n")
	b.WriteString("## Project context\n\n")
	b.WriteString("Managed by doctier — do not edit between the markers.\n")
	if len(durable) > 0 {
		b.WriteString("\nRead these for project context:\n\n")
		for _, f := range durable {
			b.WriteString("- `" + f + "`\n")
		}
	}
	if len(ephemeral) > 0 {
		b.WriteString("\nIn progress (auto-removed when the work completes):\n\n")
		for _, f := range ephemeral {
			b.WriteString("- `" + f + "`\n")
		}
	}
	if len(durable) == 0 && len(ephemeral) == 0 {
		b.WriteString("\n_No classified documents yet._\n")
	}
	b.WriteString(agentsEnd + "\n")
	return b.String()
}

// writeBlock inserts or replaces the managed block in file, creating it if
// absent. It is idempotent: re-running replaces the region between the markers
// and leaves everything else untouched.
func writeBlock(root, file, block string) error {
	path := filepath.Join(root, file)
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	content := string(existing)

	var out string
	start := strings.Index(content, agentsBegin)
	switch {
	case start >= 0:
		// Find the end marker AFTER begin, not globally: a stray end marker
		// before begin would otherwise send us down the append path and
		// duplicate the managed block (same hardening as init's ensureBlock).
		// If no end follows begin, treat begin→EOF as the corrupt block.
		tail := content[start+len(agentsBegin):]
		var rest string
		if rel := strings.Index(tail, agentsEnd); rel >= 0 {
			rest = tail[rel+len(agentsEnd):]
		}
		out = content[:start] + strings.TrimRight(block, "\n") + rest
	case strings.TrimSpace(content) == "":
		out = block
	default:
		out = strings.TrimRight(content, "\n") + "\n\n" + block
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return err
	}
	fmt.Printf("✓ updated %s\n", file)
	return nil
}
