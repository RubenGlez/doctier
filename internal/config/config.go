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
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"
)

// Manifest is the parsed .doctier.yml.
//
// The two axes (visibility, lifetime) are per-rule fields on Rule; the top-level
// keys here only carry configuration, and are named so they never collide with a
// rule's axis fields.
type Manifest struct {
	Version        int          `yaml:"version"`
	Docs           []Rule       `yaml:"docs"`
	RecipientsFile string       `yaml:"recipients_file"` // SSH-public-key recipients for private docs
	Ephemeral      EphemeralCfg `yaml:"ephemeral"`
	Policy         PolicyCfg    `yaml:"policy"`
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

type EphemeralCfg struct {
	// IntegrationBranch is where pr-merge ephemerals are collected. Empty means
	// auto-detect (origin/HEAD, else main/master).
	IntegrationBranch string `yaml:"integration_branch"`
}

type PolicyCfg struct {
	Uncovered string `yaml:"uncovered"` // allow (default) | warn | block
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
	// A future format bump must not load silently under v1 semantics — in a
	// fail-closed tool, misreading the policy is worse than refusing it.
	if m.Version != 1 {
		return nil, fmt.Errorf("manifest must declare 'version: 1' (got %d)", m.Version)
	}
	m.applyDefaults()
	if err := m.validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

func (m *Manifest) applyDefaults() {
	if m.RecipientsFile == "" {
		m.RecipientsFile = ".doctier/recipients.txt"
	}
	if m.Policy.Uncovered == "" {
		m.Policy.Uncovered = "allow"
	}
	// A sensitive file is local scratch; its natural life is the worktree, so
	// default its expiry there instead of forcing the user to spell it out.
	for i := range m.Docs {
		r := &m.Docs[i]
		if r.Sensitive && r.Lifetime == "ephemeral" && r.Expire == nil {
			r.Expire = &Expire{On: "worktree"}
		}
		// The worktree trigger needs a scope; default it to worktree so
		// validation and gc can rely on it being set.
		if r.Expire != nil && r.Expire.On == "worktree" && r.Expire.Scope == "" {
			r.Expire.Scope = "worktree"
		}
	}
}

func (m *Manifest) validate() error {
	switch m.Policy.Uncovered {
	case "allow", "warn", "block":
	default:
		return fmt.Errorf("policy.uncovered must be allow|warn|block, got %q", m.Policy.Uncovered)
	}
	for i, r := range m.Docs {
		// A malformed glob must fail loudly. doublestar.Match swallows pattern
		// errors as "no match", so an unvalidated typo like "docs/[strategy/**"
		// would silently match nothing — turning a private rule into a plaintext
		// (public) one with no signal.
		if r.Path == "" {
			return fmt.Errorf("docs[%d]: path must not be empty", i)
		}
		if !doublestar.ValidatePattern(r.Path) {
			return fmt.Errorf("docs[%d] (%q): invalid glob pattern", i, r.Path)
		}
		// Private and sensitive rules are enforced through .gitattributes /
		// .gitignore lines, and those dialects cannot express whitespace: the
		// generated line is dead, the filter/ignore never attaches, and the
		// file would stage as plaintext. Reject loudly instead.
		if strings.ContainsAny(r.Path, " \t") && (r.Visibility == "private" || r.Sensitive) {
			return fmt.Errorf("docs[%d] (%q): private/sensitive rule paths must not contain whitespace (.gitattributes/.gitignore cannot express them)", i, r.Path)
		}
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
			// scope selects the lifetime binding for the worktree trigger; it is
			// meaningless for the others.
			if r.Expire.On != "worktree" && r.Expire.Scope != "" {
				return fmt.Errorf("docs[%d] (%q): expire.scope is only valid with expire.on=worktree", i, r.Path)
			}
			if r.Expire.On == "worktree" {
				switch r.Expire.Scope {
				case "worktree":
					// Collected on `git worktree remove`, which only works for a
					// local file: a tracked file lives in the branch, not a
					// worktree, so it could never be collected there.
					if !r.Sensitive {
						return fmt.Errorf("docs[%d] (%q): expire.scope=worktree requires sensitive:true (a tracked file lives in the branch, not a worktree)", i, r.Path)
					}
				case "branch":
					// Tracked and collected when its branch merges; a local
					// (never-committed) file has no branch identity in git.
					if r.Sensitive {
						return fmt.Errorf("docs[%d] (%q): expire.scope=branch is tracked, not local; remove sensitive:true", i, r.Path)
					}
				default:
					return fmt.Errorf("docs[%d] (%q): expire.scope must be worktree|branch, got %q", i, r.Path, r.Expire.Scope)
				}
			}
		default:
			return fmt.Errorf("docs[%d] (%q): lifetime must be durable|ephemeral, got %q", i, r.Path, r.Lifetime)
		}
		// sensitive keeps a file out of git; only meaningful on ephemerals, which
		// are the only rules that can be local-only. On a durable rule it would be
		// silently ignored (the file is always tracked), so reject it loudly.
		if r.Sensitive && r.Lifetime != "ephemeral" {
			return fmt.Errorf("docs[%d] (%q): sensitive is only valid on ephemeral rules", i, r.Path)
		}
		// A sensitive file is never committed, so a merge-based trigger can never
		// collect it; only worktree (dies with the worktree) or ttl (disk sweep).
		if r.Sensitive && r.Expire != nil && r.Expire.On == "pr-merge" {
			return fmt.Errorf("docs[%d] (%q): sensitive files are never committed; expire.on must be worktree or ttl, not pr-merge", i, r.Path)
		}
		// Unreachable rule: an earlier pattern already covers this one, so with
		// first-match-wins this rule can never fire (a dangerous silent misclass).
		// This is a HEURISTIC: it tests the earlier pattern against the later one as
		// a literal path, so it catches whole-rule shadowing (identical or prefix
		// patterns) but not *partial* overlap — e.g. an earlier `**/*.md` before a
		// later `docs/**` is not flagged, yet every .md under docs/ takes the earlier
		// rule. Treat a clean load as "no obvious dead rule", not a proof of coverage.
		for j := 0; j < i; j++ {
			if ok, _ := doublestar.Match(m.Docs[j].Path, r.Path); ok {
				return fmt.Errorf("docs[%d] (%q) is unreachable: earlier rule docs[%d] (%q) already matches it (first-match-wins)", i, r.Path, j, m.Docs[j].Path)
			}
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

// DefaultRule is the implicit classification for a document that no rule covers:
// public + durable, i.e. plain git's default (tracked plaintext, kept forever).
// Users only write rules for the exceptions (private or ephemeral docs).
func DefaultRule() Rule {
	return Rule{Path: "**/*", Visibility: "public", Lifetime: "durable"}
}

// Effective returns the classification doctier applies to path: the first
// matching rule, or DefaultRule when none matches. covered reports whether an
// explicit rule matched (used only by the opt-in policy.uncovered=block gate).
func (m *Manifest) Effective(path string) (rule Rule, covered bool) {
	if r, ok := m.Match(path); ok {
		return r, true
	}
	return DefaultRule(), false
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

// BranchScoped reports whether the document is a tracked ephemeral collected
// when its feature branch merges (expire.on=worktree, scope=branch).
func (r Rule) BranchScoped() bool {
	return r.Lifetime == "ephemeral" && r.Expire != nil &&
		r.Expire.On == "worktree" && r.Expire.Scope == "branch"
}
