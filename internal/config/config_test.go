package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// prepare mirrors Load's post-parse steps so validate sees defaults applied.
func prepare(m *Manifest) error {
	m.applyDefaults()
	return m.validate()
}

func TestMatchFirstWins(t *testing.T) {
	m := &Manifest{Docs: []Rule{
		{Path: "docs/strategy/**", Visibility: "private", Lifetime: "durable"},
		{Path: "docs/**", Visibility: "public", Lifetime: "durable"},
	}}
	r, ok := m.Match("docs/strategy/roadmap.md")
	if !ok || r.Visibility != "private" {
		t.Fatalf("expected first (private) rule to win, got %+v ok=%v", r, ok)
	}
	r, ok = m.Match("docs/architecture.md")
	if !ok || r.Visibility != "public" {
		t.Fatalf("expected public rule, got %+v ok=%v", r, ok)
	}
}

func TestEffectiveDefaultsToPublicDurable(t *testing.T) {
	m := &Manifest{}
	r, covered := m.Effective("anything.md")
	if covered {
		t.Fatal("uncovered doc should report covered=false")
	}
	if r.Visibility != "public" || r.Lifetime != "durable" {
		t.Fatalf("uncovered default must be public+durable, got %+v", r)
	}
	if def := DefaultRule(); def.Visibility != "public" || def.Lifetime != "durable" {
		t.Fatalf("DefaultRule must be public+durable, got %+v", def)
	}
}

func TestEncryptedAndLocalOnly(t *testing.T) {
	priv := Rule{Visibility: "private", Lifetime: "durable"}
	if !priv.Encrypted() || priv.LocalOnly() {
		t.Fatalf("private durable: want Encrypted=true LocalOnly=false, got %v/%v", priv.Encrypted(), priv.LocalOnly())
	}
	sens := Rule{Visibility: "private", Lifetime: "ephemeral", Sensitive: true}
	if sens.Encrypted() || !sens.LocalOnly() {
		t.Fatalf("sensitive ephemeral: want Encrypted=false LocalOnly=true, got %v/%v", sens.Encrypted(), sens.LocalOnly())
	}
	pub := Rule{Visibility: "public", Lifetime: "durable"}
	if pub.Encrypted() || pub.LocalOnly() {
		t.Fatalf("public durable must be neither encrypted nor local-only")
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		m       *Manifest
		wantErr string // substring; "" means expect success
	}{
		{"valid", &Manifest{Docs: []Rule{
			{Path: "docs/strategy/**", Visibility: "private", Lifetime: "durable"},
			{Path: "docs/**", Visibility: "public", Lifetime: "durable"},
			{Path: "**/*.prd.md", Visibility: "public", Lifetime: "ephemeral", Expire: &Expire{On: "pr-merge"}},
			{Path: "**/*.report.md", Visibility: "public", Lifetime: "ephemeral", Expire: &Expire{On: "ttl", TTLDays: 30}},
			{Path: "**/_scratch/**", Visibility: "private", Lifetime: "ephemeral", Sensitive: true, Expire: &Expire{On: "worktree"}},
		}}, ""},
		{"bad visibility", &Manifest{Docs: []Rule{{Path: "a", Visibility: "secret", Lifetime: "durable"}}}, "visibility must be"},
		{"bad lifetime", &Manifest{Docs: []Rule{{Path: "a", Visibility: "public", Lifetime: "forever"}}}, "lifetime must be"},
		{"durable with expire", &Manifest{Docs: []Rule{{Path: "a", Visibility: "public", Lifetime: "durable", Expire: &Expire{On: "ttl", TTLDays: 1}}}}, "must not set expire"},
		{"ephemeral without expire", &Manifest{Docs: []Rule{{Path: "a", Visibility: "public", Lifetime: "ephemeral"}}}, "must set expire.on"},
		{"bad expire.on", &Manifest{Docs: []Rule{{Path: "a", Visibility: "public", Lifetime: "ephemeral", Expire: &Expire{On: "someday"}}}}, "expire.on must be"},
		{"ttl without days", &Manifest{Docs: []Rule{{Path: "a", Visibility: "public", Lifetime: "ephemeral", Expire: &Expire{On: "ttl"}}}}, "ttl_days > 0"},
		{"worktree without sensitive", &Manifest{Docs: []Rule{{Path: "a", Visibility: "private", Lifetime: "ephemeral", Expire: &Expire{On: "worktree"}}}}, "requires sensitive"},
		{"branch scope tracked", &Manifest{Docs: []Rule{{Path: "a", Visibility: "public", Lifetime: "ephemeral", Expire: &Expire{On: "worktree", Scope: "branch"}}}}, ""},
		{"branch scope with sensitive", &Manifest{Docs: []Rule{{Path: "a", Visibility: "private", Lifetime: "ephemeral", Sensitive: true, Expire: &Expire{On: "worktree", Scope: "branch"}}}}, "tracked, not local"},
		{"bad scope value", &Manifest{Docs: []Rule{{Path: "a", Visibility: "public", Lifetime: "ephemeral", Expire: &Expire{On: "worktree", Scope: "repo"}}}}, "expire.scope must be"},
		{"scope on non-worktree trigger", &Manifest{Docs: []Rule{{Path: "a", Visibility: "public", Lifetime: "ephemeral", Expire: &Expire{On: "ttl", TTLDays: 5, Scope: "branch"}}}}, "only valid with expire.on=worktree"},
		{"sensitive on durable", &Manifest{Docs: []Rule{{Path: "a", Visibility: "private", Lifetime: "durable", Sensitive: true}}}, "sensitive is only valid"},
		{"sensitive pr-merge", &Manifest{Docs: []Rule{{Path: "a", Visibility: "private", Lifetime: "ephemeral", Sensitive: true, Expire: &Expire{On: "pr-merge"}}}}, "not pr-merge"},
		{"unreachable rule", &Manifest{Docs: []Rule{
			{Path: "docs/**", Visibility: "public", Lifetime: "durable"},
			{Path: "docs/strategy/**", Visibility: "private", Lifetime: "durable"},
		}}, "unreachable"},
		{"bad policy", &Manifest{Policy: PolicyCfg{Uncovered: "blok"}}, "policy.uncovered must be"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := prepare(c.m)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("expected error containing %q, got %v", c.wantErr, err)
			}
		})
	}
}

func TestBranchScopedAndScopeDefault(t *testing.T) {
	// A branch-scoped ephemeral is tracked (not local-only) and reports BranchScoped.
	m := &Manifest{Docs: []Rule{
		{Path: "**/*.plan.md", Visibility: "public", Lifetime: "ephemeral", Expire: &Expire{On: "worktree", Scope: "branch"}},
		{Path: "**/_scratch/**", Visibility: "private", Lifetime: "ephemeral", Sensitive: true, Expire: &Expire{On: "worktree"}},
	}}
	if err := prepare(m); err != nil {
		t.Fatalf("valid manifest, got %v", err)
	}
	branch, scratch := m.Docs[0], m.Docs[1]
	if !branch.BranchScoped() || branch.LocalOnly() {
		t.Fatalf("branch-scoped rule: want BranchScoped=true LocalOnly=false, got %v/%v", branch.BranchScoped(), branch.LocalOnly())
	}
	if scratch.BranchScoped() {
		t.Fatal("worktree-scoped sensitive rule must not report BranchScoped")
	}
	// An omitted scope on a worktree trigger defaults to worktree.
	if scratch.Expire.Scope != "worktree" {
		t.Fatalf("omitted scope should default to worktree, got %q", scratch.Expire.Scope)
	}
}

func TestSensitiveDefaultsExpireToWorktree(t *testing.T) {
	m := &Manifest{Docs: []Rule{
		{Path: "**/_scratch/**", Visibility: "private", Lifetime: "ephemeral", Sensitive: true},
	}}
	if err := prepare(m); err != nil {
		t.Fatalf("sensitive rule without expire should be valid, got %v", err)
	}
	if m.Docs[0].Expire == nil || m.Docs[0].Expire.On != "worktree" {
		t.Fatalf("expected expire to default to worktree, got %+v", m.Docs[0].Expire)
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".doctier.yml")
	if err := os.WriteFile(path, []byte("version: 1\ndocs: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if m.Policy.Uncovered != "allow" {
		t.Errorf("default policy.uncovered = %q, want allow", m.Policy.Uncovered)
	}
	if m.RecipientsFile != ".doctier/recipients.txt" {
		t.Errorf("default recipients_file = %q, want .doctier/recipients.txt", m.RecipientsFile)
	}
}
