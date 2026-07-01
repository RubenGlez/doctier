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
	durable, ephemeral := classifiedDocs(manifestFixture(), root, files)

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
	durable, _ := classifiedDocs(manifestFixture(), root, []string{"secret.md"})
	if len(durable) != 0 {
		t.Fatalf("ciphertext private must be skipped, got %v", durable)
	}
}

func TestWriteBlockIsIdempotent(t *testing.T) {
	root := t.TempDir()
	// pre-existing hand-written content must be preserved.
	os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# My project\n\nHand-written notes.\n"), 0o644)

	block := renderBlock([]string{"docs/arch.md"}, []string{"feature.prd.md"})
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
	block2 := renderBlock([]string{"docs/arch.md", "docs/adr.md"}, nil)
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
