// Package config loads and interprets the .doctier.yml manifest.
//
// The manifest is the single source of truth for how a project classifies its
// documents. doctier itself has no opinion about specific files: the user
// declares, per glob pattern, two independent axes — visibility (public|private)
// and lifetime (durable|ephemeral) — plus the expiry trigger for ephemerals.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"
)

// Manifest is the parsed .doctier.yml.
type Manifest struct {
	Version    int           `yaml:"version"`
	Docs       []Rule        `yaml:"docs"`
	Visibility VisibilityCfg `yaml:"visibility"`
	Lifetime   LifetimeCfg   `yaml:"lifetime"`
	Policy     PolicyCfg     `yaml:"policy"`
}

// Rule maps a glob pattern to a point on both classification axes.
type Rule struct {
	Path       string  `yaml:"path"`
	Visibility string  `yaml:"visibility"` // public | private
	Lifetime   string  `yaml:"lifetime"`   // durable | ephemeral
	Sensitive  bool    `yaml:"sensitive"`  // ephemeral only: never committed, local to the worktree
	Expire     *Expire `yaml:"expire"`
}

// Expire describes when an ephemeral document is collected.
type Expire struct {
	On      string `yaml:"on"`       // pr-merge | worktree | ttl
	TTLDays int    `yaml:"ttl_days"` // used when On == "ttl"
	Scope   string `yaml:"scope"`    // used when On == "worktree": worktree | branch
}

type VisibilityCfg struct {
	Private PrivateCfg `yaml:"private"`
}

type PrivateCfg struct {
	Backend        string `yaml:"backend"`         // age (default) | repo-separado
	RecipientsFile string `yaml:"recipients_file"` // path to age/SSH recipients
}

type LifetimeCfg struct {
	Ephemeral EphemeralCfg `yaml:"ephemeral"`
}

type EphemeralCfg struct {
	DefaultScope string `yaml:"default_scope"` // worktree (default) | branch
}

type PolicyCfg struct {
	Uncovered string `yaml:"uncovered"` // block (default) | warn | allow
}

// DefaultPath is where the manifest lives, relative to the repo root.
const DefaultPath = ".doctier.yml"

// Load reads and validates the manifest at path.
func Load(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m Manifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	m.applyDefaults()
	if err := m.validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

func (m *Manifest) applyDefaults() {
	if m.Visibility.Private.Backend == "" {
		m.Visibility.Private.Backend = "age"
	}
	if m.Visibility.Private.RecipientsFile == "" {
		m.Visibility.Private.RecipientsFile = ".doctier/recipients.txt"
	}
	if m.Lifetime.Ephemeral.DefaultScope == "" {
		m.Lifetime.Ephemeral.DefaultScope = "worktree"
	}
	if m.Policy.Uncovered == "" {
		m.Policy.Uncovered = "block"
	}
}

func (m *Manifest) validate() error {
	for i, r := range m.Docs {
		switch r.Visibility {
		case "public", "private":
		default:
			return fmt.Errorf("docs[%d] (%q): visibility must be public|private, got %q", i, r.Path, r.Visibility)
		}
		switch r.Lifetime {
		case "durable":
			if r.Expire != nil {
				return fmt.Errorf("docs[%d] (%q): durable rule must not set expire", i, r.Path)
			}
		case "ephemeral":
			if r.Expire == nil {
				return fmt.Errorf("docs[%d] (%q): ephemeral rule must set expire.on", i, r.Path)
			}
			switch r.Expire.On {
			case "pr-merge", "worktree", "ttl":
			default:
				return fmt.Errorf("docs[%d] (%q): expire.on must be pr-merge|worktree|ttl, got %q", i, r.Path, r.Expire.On)
			}
			if r.Expire.On == "ttl" && r.Expire.TTLDays <= 0 {
				return fmt.Errorf("docs[%d] (%q): ttl expiry requires ttl_days > 0", i, r.Path)
			}
		default:
			return fmt.Errorf("docs[%d] (%q): lifetime must be durable|ephemeral, got %q", i, r.Path, r.Lifetime)
		}
	}
	return nil
}

// Match returns the first rule whose glob matches path (first-match-wins).
// path is slash-separated and relative to the repo root. ok is false when no
// rule matches (an "uncovered" document).
func (m *Manifest) Match(path string) (Rule, bool) {
	path = filepath.ToSlash(path)
	for _, r := range m.Docs {
		if ok, _ := doublestar.Match(r.Path, path); ok {
			return r, true
		}
	}
	return Rule{}, false
}

// Encrypted reports whether a matched rule's content is stored encrypted in git
// (private + tracked). Sensitive ephemerals are never tracked, so they are not
// encrypted-in-git.
func (r Rule) Encrypted() bool {
	return r.Visibility == "private" && !r.LocalOnly()
}

// LocalOnly reports whether the document must never be committed (gitignored,
// local to the worktree). Only sensitive ephemerals are local-only.
func (r Rule) LocalOnly() bool {
	return r.Lifetime == "ephemeral" && r.Sensitive
}
