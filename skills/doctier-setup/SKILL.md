---
name: doctier-setup
description: Interactively classify a repo's generated documents for doctier — decide which docs are private (encrypted) vs public and which are ephemeral (auto-collected) vs durable, then write .doctier.yml. Use when the user wants to set up doctier, configure .doctier.yml, classify their PRDs/specs/plans/ADRs, or decide the visibility/lifecycle of generated docs. Scans the actual repo instead of assuming filenames, and only asks about the exceptions.
---

# doctier setup

Guide the user through classifying the documents their workflow generates, on
doctier's two axes — **visibility** (`public` | `private`) and **lifetime**
(`durable` | `ephemeral`) — and write the result to `.doctier.yml`.

## Core principle: classify only the exceptions

doctier treats any path that matches no rule as **`public` + `durable`** (plain
git's default). So you do **not** classify every file. Your whole job is to help
the user carve out the two kinds of exception:

1. What is **sensitive** here? → `private` (encrypted with age)
2. What is **throwaway** here? → `ephemeral` (auto-collected at a trigger)

Everything else stays at the safe default. This keeps the setup fast and keeps
you from imposing a taxonomy on a landscape that changes monthly.

## Not opinionated

- **Scan the real repo. Don't assume canonical filenames.** Teams use different
  conventions; discover what actually exists.
- Any suggestion you make is a **hint, not a rule** — always let the user override,
  and say when a default is "common practice" vs a real standard.
- The doctier binary ships no built-in taxonomy on purpose. The hints live in
  `reference/sdd-doc-types.md` (this skill), which is editable and can go stale —
  treat it as a prompt for good questions, not ground truth.

## Procedure

**0. Preconditions.** Confirm you are in a git repo. Check doctier is installed
(`doctier --help`); if not, point the user to the README quick start. If a
`.doctier.yml` already exists, you are **editing** it, not starting over — read it
first and preserve existing rules.

**1. Discover.** List the documents the repo actually contains (markdown and doc
directories: `docs/`, `adr/`, `.specify/`, `specs/`, `*.md`, `*.prd.md`, etc.).
Cluster them by directory and filename pattern into a handful of groups. Read
`reference/sdd-doc-types.md` to *recognize* common types, but report what you
found, not what you expected.

**2. Understand posture (one key question).** The only genuinely ambiguous axis is
the lifetime of specs/PRDs — the industry is split (spec-first = ephemeral vs
spec-anchored/spec-as-source = durable). Ask the user once:

> "Do you keep your specs as a living source of truth, or discard them once the
> code exists?"

Also ask if any area is sensitive (product strategy, business context, customer
data) — that's what should be `private`.

**3. Propose the exceptions only.** For each discovered group, propose `private`
and/or `ephemeral` **only where it applies**, with a one-line reason, and leave
everything else at the default. Use `reference/sdd-doc-types.md` for informed
suggestions, framed as suggestions. Present the proposal and iterate with the user
until they confirm. Prefer few, broad glob rules over many narrow ones.

**4. Write `.doctier.yml`.** Emit a rule per confirmed exception. Rules:
- First-match-wins → put **narrower globs before broader ones** (the tool errors
  on an unreachable rule, so order matters).
- No base `**/*` rule needed (uncovered = public/durable).
- `ephemeral` needs `expire.on`: `pr-merge` (dies when the PR merges),
  `worktree`, or `ttl` (`ttl_days: N`).
- Truly secret scratch that must never enter git → `sensitive: true` (implies
  `ephemeral`, defaults to `expire.on: worktree`).
- If any rule is `private`, ensure `recipients_file: .doctier/recipients.txt` and
  tell the user to run `doctier grant "$(cat ~/.ssh/id_ed25519.pub)"`.

**5. Validate.** Run `doctier status` (show the effective classification back to the
user) and `doctier check`. Fix any violation before finishing.

**6. Offer the AGENTS.md bridge.** Optionally run `doctier agents --write` so the
classified context docs are surfaced to coding agents via AGENTS.md.

## Example output

```yaml
version: 1
docs:
  - path: "docs/strategy/**"      # sensitive → private
    visibility: private
    lifetime: durable
  - path: "**/*.prd.md"           # per-feature PRD, discarded on merge
    visibility: public
    lifetime: ephemeral
    expire: { on: pr-merge }
recipients_file: .doctier/recipients.txt
```

Keep it this small. If the user has no exceptions, it is correct to write almost
nothing — everything defaulting to public/durable is a valid, complete setup.
