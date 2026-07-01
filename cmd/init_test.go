package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rubenglez/doctier/internal/config"
)

func privateRule(path string) config.Rule {
	return config.Rule{Path: path, Visibility: "private", Lifetime: "durable"}
}

func TestEnsureAttributesRegeneratesOnRuleChange(t *testing.T) {
	root := t.TempDir()
	attrs := filepath.Join(root, ".gitattributes")
	if err := os.WriteFile(attrs, []byte("*.png binary\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := &config.Manifest{Docs: []config.Rule{privateRule("docs/a/**")}}
	if err := ensureAttributes(root, m); err != nil {
		t.Fatal(err)
	}

	// Rules changed: the managed block must follow, not stay frozen at its
	// first version (a private rule missing from .gitattributes never encrypts).
	m.Docs = []config.Rule{privateRule("docs/b/**")}
	if err := ensureAttributes(root, m); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(attrs)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Contains(got, "docs/a/**") {
		t.Fatal("stale rule must be dropped from the managed block")
	}
	if !strings.Contains(got, "docs/b/** filter=doctier diff=doctier") {
		t.Fatal("new rule must be in the managed block")
	}
	if !strings.Contains(got, "*.png binary") {
		t.Fatal("content outside the markers must be preserved")
	}

	// No private rules left: the managed block goes away entirely.
	m.Docs = nil
	if err := ensureAttributes(root, m); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(attrs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), blockBegin) {
		t.Fatal("an empty rule set must remove the managed block")
	}
	if !strings.Contains(string(data), "*.png binary") {
		t.Fatal("content outside the markers must survive block removal")
	}
}

func TestEnsureBlockIsIdempotent(t *testing.T) {
	root := t.TempDir()
	m := &config.Manifest{Docs: []config.Rule{privateRule("docs/a/**")}}
	for i := 0; i < 3; i++ {
		if err := ensureAttributes(root, m); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, ".gitattributes"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "docs/a/**"); n != 1 {
		t.Fatalf("re-running init must not duplicate the block, rule appears %d times:\n%s", n, data)
	}
}
