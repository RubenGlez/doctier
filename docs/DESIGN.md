# Design: privacy and lifecycle of generated documents

> Design document for **`doctier`**: a **standalone CLI, agnostic to any harness,
> living in its own repository** and working on top of git. It classifies the documents that a
> workflow generates according to **two independent axes —visibility and lifetime— defined
> by the user in a configuration file**, and enforces that classification
> automatically. Designed from the ground up to work with **git worktrees** (parallel coding
> agents). It includes no implementation; its purpose is to agree on the approach before building.
>
> The decisions made so far are summarized in §13.

## 1. Problem

An agent workflow generates documentation throughout development: product strategy,
architecture, decisions, QA reports, PRDs ahead of a feature, prototype
notes… Today, in the harness that motivates this design, **everything is stored in `.harness/` and is
gitignored wholesale**: no document travels through git, so nothing is backed up or shared,
and everything is local.

That doesn't fit what's needed. Different documents have different needs along **two
dimensions that are independent of each other**:

- **Who can read them** — some are safe to share (engineering, ADRs); others are the
  competitive advantage and must not be public (product strategy).
- **How long they should live** — some are permanent; others are transient (a PRD that's created
  ahead of a feature, used during development and verification, and must then disappear).

### Root cause

Git models **a single axis with only two states**: a file is tracked (visible to
anyone with the repo) or ignored (local). There's no native "private but shared",
nor "deletes itself when a condition is met". `doctier` builds the **two missing axes
on top of git**, without depending on any harness.

## 2. Goals and non-goals

**Goals**
- Model classification as **two independent axes**: visibility × lifetime (§3).
- Let **the user** decide, in a configuration file, which patterns fall into each value of
  each axis (§4). The tool **has no opinion about specific files**.
- Enforce the classification automatically, **fail-closed**: make it impossible to
  accidentally publish private content or leave an ephemeral uncollected.
- **Agnostic**: standalone CLI in its own repo; any git project can adopt it. The harness
  is just one more consumer (§11).
- **Host-agnostic**: the same on GitHub, GitLab or self-hosted, public or private repo.
- **Worktrees as a first-class case** (parallel agents, each in its worktree) (§8).

**Non-goals**
- It's not a document manager or a wiki. It only classifies and enforces policies.
- It's not a collaboration/comments tool (STRATEGY.md §5); that would dilute the focus.
- It doesn't replace the host's access control for public content; it complements it.
- It doesn't guarantee hiding the *existence* of private content, only that it can't be read (see §6, metadata).
- It doesn't support an alternative encryption backend (separate private repo): discarded for breaking the
  differentiators (§6, STRATEGY.md §4).

## 3. Model: two independent axes

The central conceptual piece. Each document (or path pattern) is described by **two
orthogonal properties**:

### Axis A — Visibility (who can read the content)

- **`public`** — plaintext. If tracked in git, anyone with the repo can read it.
- **`private`** — encrypted (`age` backend by default, §6). Only someone with the key can read it,
  even if the blob sits in an accessible repo.

### Axis B — Lifetime (how long the file lives)

- **`durable`** — indefinite lifetime. Persists until someone changes or deletes it by hand.
- **`ephemeral`** — **finite lifetime**: **deleted automatically** when its
  trigger fires. **Ephemeral does NOT mean gitignored** — it means scheduled deletion. An ephemeral
  can be perfectly tracked (and therefore travel through git) during its lifetime, and then
  disappear. Triggers (§7): `pr-merge`, `worktree`, `ttl`.

### The full matrix

The two axes combine freely; all four cells are valid:

| | **Durable** | **Ephemeral** (finite lifetime) |
|---|---|---|
| **Public** | Always tracked. *E.g.: architecture, ADRs.* | Tracked; travels in git; deleted on trigger. *E.g.: a PRD that goes in the PR and disappears on merge.* |
| **Private** | Encrypted + always tracked. *E.g.: product strategy.* | Encrypted + tracked; deleted on trigger. *E.g.: a strategic note for a feature.* |

**Important consequence for worktrees:** by decoupling lifetime from "being tracked", both
durable and ephemeral *tracked* files travel on their own to each worktree via git itself. The problem
of "seeding" a worktree almost disappears (§8).

**Exception for the sensitive case (§7.3):** an ephemeral marked as sensitive **is never committed**
(gitignored + local to the worktree), so as not to leave a trace in git history. It's the only
combination that is local by design.

## 4. The configuration file (user-driven)

A single declarative file at the repo root, `.doctier.yml`, versioned. **The user
decides everything**: which paths are public or private, which durable or ephemeral, and —if they're
ephemeral— with which trigger and which parameters. The tool only reads this file.

You only write rules for the **exceptions**: a document that matches no
rule is `public` + `durable`, git's default behavior (tracked in
cleartext, forever). No base `**/*` rule is needed.

```yaml
version: 1

# Each rule: a glob pattern + its two axes. The first rule that matches wins.
docs:
  - path: "docs/strategy/**"
    visibility: private          # encrypted with age
    lifetime: durable

  - path: "**/*.prd.md"
    visibility: public
    lifetime: ephemeral
    expire:
      on: pr-merge               # deleted when the PR merges

  - path: "docs/strategy/*.wip.md"
    visibility: private
    lifetime: ephemeral
    expire:
      on: ttl
      ttl_days: 30

  - path: "**/_scratch/**"
    visibility: private
    lifetime: ephemeral
    sensitive: true              # never committed: gitignored + local to the worktree (§7.3)
                                 # default expire: on: worktree (ttl can be set)

# Configuration (top-level keys; named so they don't collide with the
# per-rule axes 'visibility' and 'lifetime')
recipients_file: .doctier/recipients.txt   # who can read the private content (managed with grant)

# ephemeral: integration_branch is where pr-merge ephemerals are collected;
# omit it to autodetect (origin/HEAD, else main/master).
# ephemeral:
#   integration_branch: main

# Optional strictness (default: allow). If enabled, a doc that matches
# no rule blocks the commit, forcing explicit classification.
# policy:
#   uncovered: block
```

Key design point: **changing the privacy backend, or reclassifying a file, is editing
this file**. It doesn't change how the harness (or any other consumer) *writes* the documents; it only
changes what `doctier` does with them.

## 5. Mechanism derived from each combination

`doctier` translates each rule into git primitives:

| Visibility | Lifetime | Mechanism |
|---|---|---|
| public | durable | Normal tracking. |
| private | durable | Tracked with a `clean/smudge` filter (age): encrypts on `git add`, decrypts on checkout. |
| public | ephemeral | Normal tracking; a trigger (§7) does `git rm` + commit on expiry. |
| private | ephemeral (non-sensitive) | Tracked encrypted; trigger does `git rm` + commit on expiry (the ciphertext stays in history). |
| any | ephemeral + `sensitive: true` | **Never tracked**: gitignored + stored local to the worktree; deleted from disk on expiry. No trace in history. |

## 6. Private tier: encryption with age

Private content is persisted and shared with authorized parties but never read in cleartext from the
repo. The mechanism is **`age`, in-place encryption**. Two options were evaluated; below is the comparison and
why the other was left as a non-goal.

> **Decision:** `age` is the only mechanism. The manifest **exposes no backend selector** — a
> separate private repo (Option B) was discarded for clashing with `doctier`'s differentiators
> (see below and STRATEGY.md §4).

### Option A — In-place encryption (age) · DEFAULT

Same repo, `clean/smudge` filter + `.gitattributes`: the stored blob is ciphertext; the
`smudge` filter decrypts on each checkout (and therefore in each worktree). Keys managed as
`age` recipients, **reusing the SSH keys** people already have (age supports them), without
a new-key ceremony. `doctier grant` adds a key to the recipients file and **re-encrypts** the
affected files; revoke = remove the line from the recipients file + re-run `grant` (re-encrypts to the
current set). There's no separate `revoke` command.

- **Pros**: a single repo, minimal friction; host-agnostic (works even in a public repo);
  atomic history alongside the code; **native worktree compatibility** (travels via checkout,
  no seeding hooks); onboarding = add a key.
- **Cons**: git history is forever → a leaked key exposes *all* historical
  versions; encrypted blobs don't diff/merge well (binary conflicts); **metadata
  leakage** (names, sizes, dates, authors, commit messages remain public);
  key management/rotation.
- `age` over `git-crypt`: modern keys and simpler multi-recipient; `git-crypt` is tied
  to GPG and poorly maintained.

### Option B — Separate private repo (considered and discarded as a non-goal)

Alternative evaluated: strategy lives in its own private git repo, kept as a sibling repo
synced by `doctier`.

- **Pros**: real separation, zero content and metadata leakage to the public repo; native host
  access and **revocable**; plaintext inside the private repo (normal diff/merge/blame); no
  key management.
- **Cons**: two repos to sync; **friction with worktrees** (you have to resolve the sibling
  repo per worktree); it's costly to keep "code + strategy change" atomic.

**Discarded as a non-goal (see STRATEGY.md §4).** A separate repo is not *tracked-encrypted
in-place*, so it **breaks `doctier`'s two defensible differentiators**: decrypt-into-context
(the agent reads cleartext locally while git stores encrypted) and worktree-native via tracked-
encrypted (private content travels on its own via checkout, no seeding). Keeping it as a "pluggable backend"
dilutes the focus; that's why the manifest **exposes no backend selector**. If hard isolation
or real host-based revocation is ever needed, it would be reevaluated then.

### Why age

The primary case is a team repo (private) where the goal is that *not everyone with
access reads the strategy* and that *it doesn't leak to forks/mirrors*. There `age` wins: less friction,
one repo, and —decisive here— **native worktree compatibility**.

## 7. Ephemeral lifetime: triggers, scope and deletion

"Ephemeral" = finite lifetime. `doctier` supports three expiration triggers, chosen per rule in
`expire.on`:

### 7.1 Triggers

- **`pr-merge`** — deleted when the PR/branch merges. Ideal for a PRD that must exist and
  travel *inside* the PR (reviewable), and disappear once the feature is integrated. Detecting the
  merge is intrinsically a host concern (squash merges leave no merge commit; the branch
  may be deleted on the remote), so `doctier` stays agnostic with a **generic
  `doctier gc` command** invoked from several places: **CI as primary** (an action on the merge
  event; example recipes per host are shipped), **local hook as reinforcement**, and **TTL as a final
  net** in case both fail.
- **`worktree`** — lives as long as the worktree exists; collected on `git worktree remove`.
  For a specific agent's scratch.
- **`ttl`** — expires after `ttl_days` days. Safety net and for material with a natural expiry.

`doctier gc` centralizes collection: it purges ephemerals from branches/worktrees that are gone and those
that exceed their TTL. It can be invoked from hooks, CI or by hand.

### 7.2 Scope (decision: configurable, worktree by default)

For `on: worktree`, two lifetime scopes were considered:

- **`worktree`**: each worktree has its ephemerals isolated; they go away with
  `git worktree remove`. Fits parallel agents without collisions.
- **`branch`**: they're associated with the branch name; collected on merging/deleting the branch. Useful without
  worktrees, but two worktrees on the same branch would share ephemerals.

> **Prototype:** only the `worktree` scope exists, and `on: worktree` requires `sensitive: true`
> (local to the worktree): a tracked file lives on the branch, not in a worktree, so it
> couldn't be collected with `git worktree remove`. The `branch` scope is designed but not
> implemented, so the `default_scope` selector was **removed from the manifest** (it would be
> reintroduced with `branch`); for tracked ephemerals use `pr-merge` or `ttl`.

### 7.3 Deletion and git history (decision: local-only for the sensitive case)

A **tracked** ephemeral that's deleted disappears from the working tree, but **its content stays
in git history forever**. For a private one, the ciphertext remains in history
(recoverable, and exposed if a key leaks). Adopted policy, **hybrid based on
sensitivity**:

- **Non-sensitive** → tracked and deleted normally (`git rm` + commit). Stays in history:
  auditable and recoverable. It's the standard behavior and enough for most cases.
- **`sensitive: true`** → **never committed**: gitignored + local to the worktree. This way it leaves no
  trace in history. On expiry it's deleted from disk. It's the correct path for truly sensitive
  ephemeral material.

History rewriting (`filter-repo`) is discarded as an ordinary mechanism: it rewrites
shared history and is disruptive. It remains a manual emergency resource, outside the flow.

## 8. Worktree compatibility (parallel agents)

The design's reason for being: several agents working at once, each in its own `git worktree`.
Behavior per combination when creating a new worktree:

| Doc type | Does it reach the new worktree on its own? | Mechanism |
|---|---|---|
| Public/Private **durable** | **Yes**, native | Tracked; checkout brings them (private is decrypted with `smudge`). |
| Public/Private **tracked ephemeral** | **Yes**, native | Same as any tracked file; then expires by its trigger. |
| **Sensitive/local** ephemeral (`on: worktree`) | **No** (on purpose) | Gitignored → starts empty; that's correct (it's scratch for that unit of work). |

**Operational conclusion:** by moving public and private files to *tracked* files (encrypted if
appropriate), **the need for a "seeding" hook disappears** — like the current `harness-seed-worktree.sh`,
which today exists precisely because `.harness/` is gitignored wholesale. Only local
scratch keeps starting empty, which is the desired behavior.

**Isolation and collection:** local scratch lives inside the worktree directory, so
two agents don't collide and `git worktree remove` takes it away. `doctier gc` covers abandoned
worktrees (`git worktree prune`) and TTL covers orphans.

**Policy consistency:** `.doctier.yml` and `.gitattributes` are tracked → all
worktrees share the same policy without manual syncing. The `age` key is per
machine/user, valid for all its worktrees.

## 9. The CLI and its distribution (decision: standalone in its own repo)

`doctier` is a **standalone CLI in its own repository**, written in **Go** (single
static binary: no runtime dependencies, instant startup in the clean/smudge filters, and
trivial distribution via brew / direct download / `go install`). Any git project adopts it
with `doctier init`. It doesn't drag per-project script copies around.

| Command | What it does |
|---|---|
| `doctier init` | Scaffolding: `.doctier.yml`, `.gitattributes`/`.gitignore` entries, hooks and the clean/smudge filter. |
| `doctier check [--staged]` | Fail-closed: no private content in cleartext, no sensitive file staged, and (opt-in) no unclassified doc. For pre-commit/pre-push and CI. |
| `doctier status` | Shows the effective classification of each doc and its expiry. |
| `doctier agents [--write]` | Emits a tier-aware context block for `AGENTS.md`/`CLAUDE.md`. |
| `doctier gc [--trigger T]` | Purges expired ephemerals (`ttl`/`worktree`/`pr-merge`/`all`). |
| `doctier grant "<ssh-pubkey>"` | Adds a recipient and re-encrypts private files. To **revoke**: remove the line from the recipients file and re-run `grant` (re-encrypts to the current set). |
| `doctier filter clean\|smudge <f>` | Git filter (invoked by git, not by a person). |

> `reveal`/`hide` commands (decrypt/encrypt locally to edit) were considered but are **not
> implemented**: with the smudge filter the working tree is already in cleartext, so they haven't been needed.

## 10. Safety nets (fail-closed)

The most serious failure is **publishing private content in cleartext** by accident. The
`pre-commit`/`pre-push` hook (`doctier check`) **fails the commit** if:

- A `private` path is staged **unencrypted**.
- A `sensitive` ephemeral path is staged (must never be committed).
- A doc **matches no rule** and `policy.uncovered: block` has been enabled (opt-in,
  default `allow`: uncovered ones are treated as `public` + `durable`).

The same `doctier check` runs in **CI** as the last barrier (it doesn't depend on the client having
the hooks installed). This fail-closed behavior is what makes the solution reliable: security
doesn't depend on anyone's memory.

## 11. Integration with a consumer (the harness, as an example)

`doctier` doesn't know about the harness. Adoption by a consumer is just:

1. Provide its own `.doctier.yml` classifying its paths (example in Appendix A).
2. **Replace** any wholesale gitignore of its docs with tier-aware rules.
3. **Retire/lighten** seeding hooks: durable and tracked ephemeral files already travel via git;
   only local scratch might need seeding (and by design it starts empty).
4. Optionally invoke `doctier gc` when closing a feature (or rely on the hooks/CI).

Any other git project adopts it the same way, with nothing from the harness.

## 12. Migration from the current state (of the harness)

1. Add `.doctier.yml` with the consumer's rules (Appendix A).
2. Take out of gitignore whatever becomes tracked (public durable, and private durable/ephemeral
   encrypted) and start tracking it.
3. Configure `age` for private content.
4. Keep only `sensitive` ephemerals local; wire `doctier gc` + hooks for collection.
5. Replace `harness-gitignore.sh` (ignores wholesale) and lighten `harness-seed-worktree.sh`.
6. Run `doctier check` in CI.

Incremental migration: you can start with just the private content (the most urgent) and add the rest
later.

## 13. Decisions made

1. **Private encryption**: **`age` in-place, single mechanism** (no backend selector). The separate
   private repo was discarded as a non-goal for breaking the differentiators. §6.
2. **User-driven classification**: a `.doctier.yml` where the user decides
   visibility, lifetime, trigger and TTL per pattern. The tool doesn't opine about specific
   files. §4.
3. **Two-independent-axis model**: visibility (public/private) × lifetime
   (durable/ephemeral). §3.
4. **Ephemeral scope**: only `worktree` (local, requires `sensitive`); `branch` designed but not
   implemented, no `default_scope` selector in the manifest. §7.2.
5. **Distribution**: **standalone CLI in its own repo** (outside the harness). §9.
6. **Ephemeral = finite lifetime, not gitignored**; normal deletion for non-sensitive content, and **local-only
   (never committed) for sensitive content**, so as not to leave a trace in history. §7.
7. **Ecosystem**: **Go**, single static binary (no runtime, instant startup in the
   clean/smudge filters, trivial distribution). §9.
8. **`age` keys**: **reuse the existing SSH keys** as recipients (age supports them);
   `doctier grant` adds a key and re-encrypts (revoke = remove the line and re-run `grant`). §6.
9. **`pr-merge` trigger**: generic `doctier gc` command invoked from **CI (primary)** on
   the merge event, with **local-hook reinforcement** and **TTL as a final net**. §7.1.
10. **Manifest**: **YAML**, with **"first rule that matches"** precedence (explicit order
    controlled by the user). §4.

## 14. Pending implementation details (prototype phase)

No design decision is left open; what remains is construction detail:

- **Final name** of the CLI (`doctier` is a working name).
- **`age` key ceremony**: rotating the data key on revoke, custody, and the concrete
  onboarding/offboarding flow over the recipients file.
- **Detail of the local `pr-merge` hook**: how to robustly decide "this branch is already merged/gone"
  (squash merges, deleted remote branches). CI-based collection is already solved
  with the integration-branch gating; there are example recipes in [`ci/`](ci/) (GitHub Actions,
  GitLab CI).

---

## Appendix A — Example configuration for a harness-like consumer

> **Not part of the tool.** It's just an example of how *a* project (the harness)
> might classify its documents. Each project writes its own. The concrete values
> (e.g. whether `CONTEXT.md` or `qa/report.md` are public, private, durable or ephemeral) are
> the decision of that project's user, not of `doctier`.

```yaml
version: 1
docs:
  # No base rule: anything uncovered is public + durable by default.

  # Product strategy → private and durable
  - path: ".harness/product/**"
    visibility: private
    lifetime: durable

  # Engineering and decisions → public and durable (the default value, explicit for clarity)
  - path: ".harness/engineering/**"
    visibility: public
    lifetime: durable
  - path: ".harness/adr/**"
    visibility: public
    lifetime: durable

  # QA report → point-in-time snapshot; example as ephemeral by TTL (the user decides)
  - path: ".harness/qa/report.md"
    visibility: public
    lifetime: ephemeral
    expire: { on: ttl, ttl_days: 90 }

  # Pre-feature PRD → travels in the PR and dies on merge
  - path: ".harness/**/*.prd.md"
    visibility: public
    lifetime: ephemeral
    expire: { on: pr-merge }

  # Prototype / scratch notes → sensitive and local to the worktree
  # (sensitive expires on worktree by default)
  - path: ".harness/**/_prototype-*"
    visibility: private
    lifetime: ephemeral
    sensitive: true

recipients_file: .doctier/recipients.txt
```
