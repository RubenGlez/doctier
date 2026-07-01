# doctier

**Tiered privacy and lifecycle for generated documents, over git.** Harness-agnostic.

Git models one axis with two states: a file is tracked (public, if the repo is)
or ignored (local). `doctier` adds the two axes real projects need, driven by a
single user-owned manifest:

- **Visibility** — `public` (plaintext) · `private` (encrypted with [age](https://age-encryption.org), reusing your SSH keys)
- **Lifetime** — `durable` (forever) · `ephemeral` (finite life, auto-collected)

It was built for **coding agents working in parallel git worktrees**: tracked
public/private docs travel to every worktree through git natively (private ones
are decrypted by the smudge filter), so there is no seeding hook to maintain.

> Status: **prototype**. The core — manifest, age clean/smudge filters, fail-closed
> checks, ephemeral GC — works end-to-end. See [`docs/DESIGN.md`](docs/DESIGN.md)
> for the full design and the decisions behind it.

## How it works

The manifest `.doctier.yml` maps glob patterns to the two axes (first match wins):

```yaml
version: 1
docs:
  - path: "docs/strategy/**"      # crown jewels
    visibility: private
    lifetime: durable
  - path: "**/*.prd.md"           # a PRD that travels in the PR and dies on merge
    visibility: public
    lifetime: ephemeral
    expire: { on: pr-merge }
  - path: "**/_scratch/**"        # sensitive scratch, never committed
    visibility: private
    lifetime: ephemeral
    sensitive: true
    expire: { on: worktree }
  - path: "**/*"                  # base rule: nothing stays unclassified
    visibility: public
    lifetime: durable
visibility:
  private: { backend: age, recipients_file: .doctier/recipients.txt }
policy: { uncovered: block }
```

`doctier` turns each rule into git primitives:

| Visibility | Lifetime | Storage |
|---|---|---|
| public | durable | tracked, plaintext |
| private | durable | tracked, encrypted (age clean/smudge filter) |
| public/private | ephemeral (not sensitive) | tracked; deleted at the trigger |
| any | ephemeral + `sensitive: true` | gitignored, local to the worktree; deleted at the trigger |

## Commands

| Command | What it does |
|---|---|
| `doctier init` | Scaffold `.doctier.yml`, `.gitattributes`, `.gitignore`, hooks and the clean/smudge filter. |
| `doctier check [--staged]` | Fail-closed policy check (for pre-commit/pre-push and CI). |
| `doctier status` | Show the effective classification of each document. |
| `doctier gc [--trigger ttl\|worktree\|pr-merge\|all]` | Collect expired ephemerals. |
| `doctier grant "<ssh-pubkey>"` | Add a recipient and re-encrypt private docs. |
| `doctier filter clean\|smudge <file>` | Git filter (invoked by git, not by hand). |

## Quick start

```bash
go build -o doctier .          # or: go install github.com/rubenglez/doctier@latest
cd your-repo
doctier init
doctier grant "$(cat ~/.ssh/id_ed25519.pub)"
# edit .doctier.yml to classify your docs
doctier check
```

Decryption uses your SSH private key (`$DOCTIER_SSH_KEY`, else `~/.ssh/id_ed25519`
or `~/.ssh/id_rsa`).

## Fail-closed guarantees

`doctier check` (wired as a pre-commit hook and run in CI) refuses the commit if:

- a `private` file is staged in cleartext (filter not applied),
- a `sensitive` ephemeral is staged at all,
- a document matches no rule and `policy.uncovered: block`.

## Known limitations (prototype)

- `age` ciphertext leaves filenames, sizes and commit metadata visible; the
  content of a deleted tracked-ephemeral remains in git history (use
  `sensitive: true` for material that must leave no trace).
- The `pr-merge` trigger is host-specific to detect reliably; `doctier gc` is the
  generic command — wire it from CI (primary), a local hook (reinforcement) and
  rely on `ttl` as the safety net.
- Separate-private-repo backend is designed but not yet implemented (age only).

## License

MIT — see [LICENSE](LICENSE).
