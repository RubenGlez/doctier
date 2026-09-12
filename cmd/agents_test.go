package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rubenglez/doctier/internal/agex"
	"github.com/rubenglez/doctier/internal/config"
)

func manifestFixture() *config.Manifest {
	m := &config.Manifest{Docs: []config.Rule{
		{Path: "docs/**", Visibility: "public", Lifetime: "durable"},
		{Path: "**/*.prd.md", Visibility: "public", Lifetime: "ephemeral", Expire: &config.Expire{On: "pr-merge"}},
		{Path: "secret.md", Visibility: "private", Lifetime: "durable"},
	}}
	return m
}

func TestClassifiedDocsPartitionsAndSkipsUncovered(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "arch.md"), []byte("a"), 0o644) // will live under docs/
	os.MkdirAll(filepath.Join(root, "docs"), 0o755)
	os.WriteFile(filepath.Join(root, "docs/arch.md"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(root, "feature.prd.md"), []byte("p"), 0o644)
	os.WriteFile(filepath.Join(root, "secret.md"), []byte("plaintext strategy"), 0o644)

	files := []string{"docs/arch.md", "feature.prd.md", "secret.md", "main.go", ".doctier.yml"}
	durable, ephemeral := classifiedDocs(manifestFixture(), root, files, nil)

	if strings.Join(durable, ",") != "docs/arch.md,secret.md" {
		t.Fatalf("durable = %v, want [docs/arch.md secret.md]", durable)
	}
	if strings.Join(ephemeral, ",") != "feature.prd.md" {
		t.Fatalf("ephemeral = %v, want [feature.prd.md]", ephemeral)
	}
}

func TestClassifiedDocsSkipsCiphertextPrivate(t *testing.T) {
	root := t.TempDir()
	// secret.md present but still ciphertext (no key on this machine): skip it.
	os.WriteFile(filepath.Join(root, "secret.md"), []byte(agex.ArmorHeader+"\nZm9v\n"), 0o644)
	durable, _ := classifiedDocs(manifestFixture(), root, []string{"secret.md"}, nil)
	if len(durable) != 0 {
		t.Fatalf("ciphertext private must be skipped, got %v", durable)
	}
}

// Work-state filtering: an ephemeral is listed only while it belongs to the
// current work unit.
func TestAgentsInFlightFiltersEphemeralsByWorkState(t *testing.T) {
	manifest := `version: 1
docs:
  - path: "**/*.prd.md"
    visibility: public
    lifetime: ephemeral
    expire: { on: pr-merge }
`
	root := initRepo(t, manifest)
	write(t, root, "README.md", "x")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "init")

	m, _, err := loadManifest()
	if err != nil {
		t.Fatal(err)
	}

	// Untracked ephemeral: local work here — in flight everywhere.
	write(t, root, "local.prd.md", "wip")
	if !inFlight(m)("local.prd.md") {
		t.Fatal("an untracked ephemeral is always in flight")
	}

	// Committed on a feature branch, absent from main: in flight.
	git(t, root, "checkout", "-qb", "feat")
	write(t, root, "feature.prd.md", "plan")
	git(t, root, "add", "feature.prd.md")
	git(t, root, "commit", "-qm", "plan")
	if !inFlight(m)("feature.prd.md") {
		t.Fatal("a feature-branch ephemeral not on the integration branch is in flight")
	}

	// Merged to main, checked out main: finished work unit — stale context.
	git(t, root, "checkout", "-q", "main")
	git(t, root, "merge", "-q", "feat")
	if inFlight(m)("feature.prd.md") {
		t.Fatal("a tracked ephemeral on the integration branch is finished, not context")
	}

	// Back on the feature branch, where the doc now ALSO exists on main:
	// no longer exclusive to this work unit — stale.
	git(t, root, "checkout", "-q", "feat")
	if inFlight(m)("feature.prd.md") {
		t.Fatal("an ephemeral already on the integration branch is stale on any branch")
	}
}

// --all bypasses the work-state filter.
func TestAgentsAllListsFinishedEphemerals(t *testing.T) {
	manifest := `version: 1
docs:
  - path: "**/*.prd.md"
    visibility: public
    lifetime: ephemeral
    expire: { on: pr-merge }
`
	root := initRepo(t, manifest)
	write(t, root, "feature.prd.md", "done plan")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "on main") // on the integration branch: finished

	filtered := captureStdout(t, func() {
		if err := runAgents(nil); err != nil {
			t.Errorf("agents: %v", err)
		}
	})
	if strings.Contains(filtered, "feature.prd.md") {
		t.Fatalf("finished ephemeral must be filtered by default:\n%s", filtered)
	}
	all := captureStdout(t, func() {
		if err := runAgents([]string{"--all"}); err != nil {
			t.Errorf("agents --all: %v", err)
		}
	})
	if !strings.Contains(all, "feature.prd.md") {
		t.Fatalf("--all must list every classified ephemeral:\n%s", all)
	}
}

func TestWriteBlockIsIdempotent(t *testing.T) {
	root := t.TempDir()
	// pre-existing hand-written content must be preserved.
	os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# My project\n\nHand-written notes.\n"), 0o644)

	block := renderBlock(nil, []string{"docs/arch.md"}, []string{"feature.prd.md"})
	if err := writeBlock(root, "AGENTS.md", block); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if !strings.Contains(string(first), "Hand-written notes.") {
		t.Fatal("existing content must be preserved")
	}
	if !strings.Contains(string(first), "docs/arch.md") {
		t.Fatal("block must be inserted")
	}

	// Re-run with a changed doc set: exactly one managed block, updated content.
	block2 := renderBlock(nil, []string{"docs/arch.md", "docs/adr.md"}, nil)
	if err := writeBlock(root, "AGENTS.md", block2); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if strings.Count(string(second), agentsBegin) != 1 || strings.Count(string(second), agentsEnd) != 1 {
		t.Fatalf("must keep exactly one managed block:\n%s", second)
	}
	if !strings.Contains(string(second), "docs/adr.md") || strings.Contains(string(second), "feature.prd.md") {
		t.Fatalf("block must be replaced, not appended:\n%s", second)
	}
	if !strings.Contains(string(second), "Hand-written notes.") {
		t.Fatal("existing content must survive re-runs")
	}
}

// With primaries declared, the block lists them as entry points and collapses
// the remaining durables into per-directory lines.
func TestRenderBlockPrimariesGroupTheRest(t *testing.T) {
	block := renderBlock(
		[]string{"guide/architecture.md"},
		[]string{"guide/features/one.md", "guide/features/two.md", "notes.md"},
		[]string{"plan.prd.md"},
	)
	for _, want := range []string{
		"Entry points (read these first):",
		"`guide/architecture.md`",
		"Further docs, by directory:",
		"`guide/features/` (2 docs)",
		"repo root (1 docs)",
		"In progress (auto-removed when the work completes):",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("block missing %q:\n%s", want, block)
		}
	}
	// Individual non-primary paths must NOT be listed.
	if strings.Contains(block, "guide/features/one.md") {
		t.Fatalf("non-primaries must be grouped, not listed:\n%s", block)
	}
}

// No primaries declared: exhaustive listing, exactly as before.
func TestRenderBlockWithoutPrimariesIsExhaustive(t *testing.T) {
	block := renderBlock(nil, []string{"docs/arch.md", "secret.md"}, nil)
	if !strings.Contains(block, "Read these for project context:") ||
		!strings.Contains(block, "`docs/arch.md`") ||
		!strings.Contains(block, "`secret.md`") {
		t.Fatalf("no-primary block must list every durable doc:\n%s", block)
	}
	if strings.Contains(block, "Entry points") || strings.Contains(block, "by directory") {
		t.Fatalf("no-primary block must keep the legacy shape:\n%s", block)
	}
}
