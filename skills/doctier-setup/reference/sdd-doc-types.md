# SDD document types — hints for classification

> **These are hints, not rules.** They come from a July 2026 market snapshot of
> spec-driven development. The field moves monthly and every team has its own
> conventions, so use this to ask better questions — never to override the user.
> Re-verify periodically; edit this file freely, it ships in the skill, not the
> binary.

## The one question that actually needs asking

The lifetime of the **spec/PRD itself** is genuinely contested in the industry
(Martin Fowler's taxonomy):

- **spec-first** — the spec guides the first generation, then can be discarded →
  **ephemeral**.
- **spec-anchored / spec-as-source** (Tessl, Kiro, Spec Kit lean here) — the spec
  is a living source of truth → **durable**.

So ask: *"Do you keep specs as a living source of truth, or discard them once the
code exists?"* Everything else below has a reasonably strong default.

## Recognize files by convention (don't assume they exist)

| Tool / convention | Files |
|---|---|
| GitHub Spec Kit | `spec.md`, `plan.md`, `tasks.md`, `memory/constitution.md`, `research.md`, `data-model.md`, `contracts/`, `quickstart.md` |
| AWS Kiro | `requirements.md` (EARS syntax), `design.md`, `tasks.md` |
| Tessl | `*.spec.md` |
| Cross-vendor standards | `AGENTS.md` (Linux Foundation), ADRs (`adr/`, Nygard/MADR template) |
| Tool rules files | `CLAUDE.md`, `.cursorrules`, `.github/copilot-instructions.md` |

## Default hints by document type

Confidence = how strong the industry consensus is. Low confidence → lean harder on
asking the user.

| Document | Lifetime hint | Visibility hint | Confidence |
|---|---|---|---|
| `AGENTS.md`, constitution, rules files | durable | public | high — built to be shared |
| ADR (`adr/**`) | durable | public | high — immutable historical rationale |
| architecture / `design.md` | durable | public (in-repo) | high |
| `plan.md` | ephemeral (`pr-merge`) | public | high — regenerable from the spec |
| `tasks.md` | ephemeral (`pr-merge`) | public | high |
| research, data-model, quickstart | ephemeral (`ttl`) | public | medium |
| `spec.md` / `requirements.md` / PRD | **ask** (see posture question) | private | see caveat |
| prototype / scratch notes | ephemeral + `sensitive` (`worktree`) | private | medium |

## Honest caveats

- **Visibility defaults are the weakest part.** There is no primary-source privacy
  taxonomy in the industry. "specs/PRDs → private, ADRs/plans → public" is reasoned
  from *what the document usually contains* (product strategy and business context
  concentrate in specs/PRDs), not from a standard. Present these as opinions.
- **Only three things are truly standardized cross-vendor:** AGENTS.md (agent
  context), the Nygard/MADR ADR template, and EARS (requirements syntax). The SDD
  triads (spec/plan/tasks vs requirements/design/tasks vs `*.spec.md`) are
  proprietary conventions that merely rhyme — do not assume filenames.
- **Google's `DESIGN.md` is unrelated.** It is a design-*system* standard (UI
  tokens: colors/typography/spacing), not the SDD architecture document.

## Primary sources (July 2026)

- AGENTS.md — https://agents.md/ · Linux Foundation AAIF stewardship
- ADRs — Nygard template (cognitect.com, 2011); MADR (adr.github.io)
- EARS — https://alistairmavin.com/ears/
- GitHub Spec Kit — https://github.com/github/spec-kit/blob/main/spec-driven.md
- AWS Kiro — https://kiro.dev/docs/specs/
- Tessl (spec-anchored, Spec Registry) — https://tessl.io
- Fowler taxonomy — https://martinfowler.com/articles/exploring-gen-ai/sdd-3-tools.html
- Thoughtworks (spec durability debate) — thoughtworks.com/insights (SDD, 2025)
