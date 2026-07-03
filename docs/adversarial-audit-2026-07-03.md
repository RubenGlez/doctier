# Adversarial codebase audit — doctier (2026-07-03)

Scope: full read of `main.go`, `cmd/*`, `internal/*`, tests, `.goreleaser.yaml`,
`.github/workflows/release.yml`, `docs/ci/*`, `install.sh`, `skills/doctier-setup`,
README, and the repo's own dogfooded config (`.doctier.yml`, `.gitattributes`,
`.harness/`). Every CONFIRMED finding below was either traced end-to-end in code
or reproduced empirically against a locally built binary in sandbox repos.

Note on this report's location: the preferred location was `.harness/qa/`, but
`.harness/**` is classified `private + durable` by this repo's own `.doctier.yml`,
and this clone has no `filter.doctier` configured — committing there would
produce a *plaintext blob at a policy-private path*, which is precisely the
violation `doctier check` exists to block (it would fail for every user of the
repo, and the plaintext would be permanent in history). So the report lives in
`docs/` instead. That situation is itself evidence for findings H4 and D1 below.

Build/test status: `go build ./...` passes; `go test ./...` passes
(`cmd`, `internal/agex`, `internal/config` green; `gitx` and `main` have no tests).

---

## 1. System map

### Architecture

```
main.go              → cmd.Execute (hand-rolled dispatch, no cobra)
cmd/root.go          → usage, subcommand routing, loadManifest()
cmd/init.go          → scaffolds .doctier.yml, recipients, .gitattributes/.gitignore
                       managed blocks, git config filter.doctier.* + diff.doctier.textconv,
                       pre-commit / pre-push / post-merge hooks
cmd/filter.go        → `filter clean|smudge` (git filter), `textconv` (diff driver)
cmd/check.go         → fail-closed policy check over index blobs
cmd/status.go        → classification table + clone-setup warnings
cmd/gc.go            → ttl / pr-merge / branch / worktree collection
cmd/grant.go         → append recipient + `git add --renormalize .` re-encrypt
cmd/agents.go        → managed AGENTS.md block of classified, readable docs
internal/config      → .doctier.yml parse/validate, first-match glob (doublestar)
internal/agex        → age over SSH keys: Encrypt/Decrypt, ValidCiphertext,
                       LoadRecipients, LoadIdentity ($DOCTIER_SSH_KEY → id_ed25519 → id_rsa)
internal/gitx        → shell-outs to git (diff --cached, ls-files, cat-file --batch, …)
```

### Real execution paths

- **Encrypt (clean)**: `git add` → git runs `doctier filter clean %f` per file
  matched by the `.gitattributes` managed block → `runFilter` re-matches the path
  against the *manifest* (`cmd/filter.go:36-44`); non-private → passthrough. clean
  (`cmd/filter.go:54-79`): whole-blob `ValidCiphertext` passthrough guard → staged-blob
  reuse (idempotency; bypassed by `DOCTIER_FORCE_ENCRYPT`) → `agex.Encrypt` to the
  recipients file.
- **Decrypt (smudge)**: checkout → `doctier filter smudge %f` → `smudge`
  (`cmd/filter.go:105-124`) fails **open**: no/bad key ⇒ emit ciphertext + stderr warning.
- **Check**: pre-commit (`--staged`, `git diff --cached --diff-filter=ACMR`) or
  full (`git ls-files --cached --others --exclude-standard`); batch-fetches index
  blobs (`cmd/check.go:57`), flags: unusable recipients, staged/tracked sensitive
  ephemerals, private blobs that are not `ValidCiphertext`, and (opt-in) uncovered files.
- **GC**: `ttl` (commit date, else mtime; `git rm` for tracked, `os.Remove` for
  untracked), `pr-merge`/`branch` (both = `gcOnIntegration`: delete matching docs
  when `CurrentBranch() == integration branch`), `worktree` (only `git worktree prune`).
- **Grant/revoke**: append key (validated), then `git add --renormalize .` with
  `DOCTIER_FORCE_ENCRYPT=1` so clean re-encrypts.

### Key invariants (as promised)

1. A `private` doc's blob in git is always valid age ciphertext (fail-closed via
   hooks locally and `doctier check` in CI).
2. A `sensitive` ephemeral never reaches git.
3. Re-encryption after grant/revoke actually covers all private docs.
4. Ephemerals are collected at their trigger and never destructively on a feature branch.

Findings below show where 1, 3, and (for uncommitted files) data-safety break.

---

## 2. Findings

Severity: **CRITICAL** (policy guarantee broken / data loss) → **HIGH** → **MEDIUM** → **LOW**.
All CONFIRMED items were reproduced or fully traced; disproof attempted first.

### CRITICAL

**C1. `doctier check` silently skips any path git quotes (non-ASCII filenames) — the CI guarantee fails open.** CONFIRMED (reproduced).
`internal/gitx/gitx.go:51` (`git diff --cached --name-only`) and `:60`
(`git ls-files`) parse newline-separated output without `-z` / `core.quotepath=off`.
With default `core.quotepath=true`, git prints a path like `secret/café.md` as
`"secret/caf\303\251.md"` (quotes and octal escapes included). That quoted string
matches no manifest rule (`internal/config/config.go:183`), and its
`cat-file --batch` lookup returns "missing", so `check` treats the file as
uncovered/not-staged.
Repro: private rule `secret/**`, stage plaintext `secret/café.md` with no filter
configured → `doctier check` and `doctier check --staged` both print
`✓ doctier: policy satisfied` while `git cat-file blob ':secret/café.md'` is
`TOP SECRET`. README calls CI `check` "the only host-independent guarantee";
one accented character in a filename voids it. Also mis-lists files for
`status`, `gc`, and `agents`.
**Direction:** use `-z` (`--name-only -z`, `ls-files -z`) and NUL-split; add a
regression test with a UTF-8 filename.

**C2. The documented revoke flow can silently re-encrypt nothing while reporting success.** CONFIRMED (reproduced).
`clean` returns already-valid ciphertext unchanged *before* checking
`DOCTIER_FORCE_ENCRYPT` (`cmd/filter.go:61-63` vs `:73`). Whenever a private doc
sits as ciphertext in the worktree — keyless checkout, checkout done before
`doctier init`, passphrase-protected key, or this very repo today —
`doctier grant` (`git add --renormalize .`, `cmd/grant.go:72-78`) passes every
such file through untouched, then prints
`✓ re-encrypted private documents to the current recipient set` (`cmd/grant.go:39`).
Repro: revoke key A (recipients = key B only), run `doctier grant` on a
ciphertext worktree → staged blob still decrypts with revoked key A.
Forward-only revocation is documented; *claiming* re-encryption that did not
happen is not — the operator believes future edits are protected and they are not.
**Direction:** under `DOCTIER_FORCE_ENCRYPT`, decrypt-and-re-encrypt (requires a
key) instead of passthrough; make `grant` fail loudly when any private file
cannot be re-encrypted (no identity, ciphertext worktree), and verify afterwards
that every staged private blob decrypts under the new set.

**C3. TTL gc permanently deletes never-committed files based on filesystem mtime.** CONFIRMED (reproduced).
`gcTTL` falls back to `os.Stat` mtime when there is no commit for the path
(`cmd/gc.go:82-86`), and `removeFile` uses plain `os.Remove` for untracked files
(`cmd/gc.go:196-200`). A file that matches a ttl rule and arrived with an old
mtime — `cp -p`, `rsync -a`, archive extraction, restored backup — is deleted
unrecoverably (never in git, no trash) by `doctier gc` with the **default**
trigger `all`. Repro: `touch -d "60 days ago" reports/q2.md` (uncommitted, rule
`ttl_days: 30`) → `doctier gc --trigger ttl` deletes it. The
"uncommitted modifications survive" protection (`git rm` refusal) only covers
tracked files. For gitignored *sensitive* scratch this fallback is the design;
for ordinary tracked-pattern ephemerals it is a data-loss trap.
**Direction:** never hard-delete an untracked file matching a *non-sensitive*
rule (skip + warn, or require `--force`); consider quarantining (move to
`.git/doctier-trash`) instead of `os.Remove`.

### HIGH

**H1. A malformed glob makes a private rule silently match nothing — fail-open.** CONFIRMED (reproduced).
`Manifest.validate` never validates pattern syntax, and every `doublestar.Match`
error is discarded (`internal/config/config.go:169,183`; also
`cmd/gc.go` glob in `localOnlyTTLFiles`). Manifest with
`path: "docs/[strategy/**"` loads fine; `docs/strategy/roadmap.md` is treated as
uncovered → committed plaintext, `doctier check` passes. One typo turns
"encrypted" into "public" with zero signal (with default `policy.uncovered: allow`).
**Direction:** call `doublestar.ValidatePattern` for every rule in `validate()`
and fail load; treat `Match` errors as errors, not non-matches.

**H2. `doctier init` under a global `core.hooksPath` installs doctier's hooks machine-wide.** CONFIRMED (reproduced).
`gitx.HooksPath` (`internal/gitx/gitx.go:235-243`) falls back to
`git rev-parse --git-path hooks`, which resolves `core.hooksPath` from *any*
scope. With a global hooks dir (a common setup), `installHooks`
(`cmd/init.go:174-196`) writes `pre-commit`/`pre-push`/`post-merge` into the
user's global hooks directory. From then on **every repo on the machine** runs
`doctier check --staged` on commit; repos without `.doctier.yml` fail with
`read manifest: no such file` and all their commits are blocked until the user
finds and deletes the hooks.
**Direction:** detect that the effective hooks path is outside the repo's
`$GIT_DIR` and refuse (or make the hook scripts no-op when no `.doctier.yml`
exists: `[ -f .doctier.yml ] || exit 0`).

**H3. Reclassifying an existing tracked file as private commits plaintext past the pre-commit gate.** CONFIRMED (reproduced).
`check --staged` only examines files in `git diff --cached` (`cmd/check.go`); a
file that was already committed plaintext and is merely *reclassified* is not
staged, so the reclassification commit passes pre-commit and the plaintext blob
keeps riding in every new tree. Repro: commit `docs/strategy/plan.md` public,
add a private rule, re-run `doctier init` per README, commit →
`git cat-file blob HEAD:docs/strategy/plan.md` is plaintext. The full check
(pre-push) does catch it — after the plaintext is already in local history. The
README's remediation ("re-run `doctier init` — it syncs .gitattributes") never
mentions the required `git add --renormalize .`, nor that history retains the
plaintext forever (needs `git filter-repo`).
**Direction:** `init` (or `check`) should detect tracked-plaintext files under
new private rules and print the exact remediation; document the history caveat.

**H4. There is no way to decrypt an already-checked-out clone — the second-user onboarding path dead-ends.** CONFIRMED (reproduced).
A fresh clone checks out ciphertext (no filter configured — expected). After
`doctier init` + a valid key, nothing re-runs smudge: git considers the files
unmodified because clean(ciphertext) == index blob (the `ValidCiphertext`
passthrough, `cmd/filter.go:61`), so `git checkout -- <path>` and even
`git reset --hard` are **no-ops** — reproduced. There is no `doctier unlock`
(git-crypt's equivalent), and neither README nor `init` output mentions the
workaround (`rm` files then checkout, or `git stash && git stash pop`). Evidence:
this repo's own `.harness/**` sits as ciphertext in this worktree with no
documented path to plaintext.
**Direction:** add an `unlock`/`smudge-all` step to `init` (e.g.
`git checkout-index --force` over covered paths, or delete+checkout), and
document it in the quick start.

**H5. Pre-push "reinforcement" checks the index, not the commits being pushed.** CONFIRMED (traced).
`doctier check` (full) verifies blobs at `:path` — the index of the current
worktree (`cmd/check.go:57`, `gitx.StagedBlobs`). The pre-push hook therefore
approves a push whose *commits* contain cleartext private blobs whenever the
index has since been fixed (or when pushing a branch that is not checked out —
`git push origin other-branch` is vetted against the wrong tree entirely). The
same applies to CI recipes that check out only the PR head: intermediate commits
with plaintext are never inspected.
**Direction:** for pre-push, read the ref updates from stdin and check the trees
of pushed commits (`git rev-list old..new`, `cat-file` per private path), or at
minimum document that only the tip tree is validated.

### MEDIUM

**M1. Two glob dialects, one manifest: `doctier init` writes doublestar patterns into `.gitattributes`, which git parses as gitignore-syntax.** CONFIRMED (reproduced).
Rule `docs/{strategy,finance}/**` validates and matches in doctier, but the
generated attributes line (`cmd/init.go:141-148`) is dead in git (no brace
support) → filter never runs → plaintext staged. `check` does catch it, but the
error ("not valid age ciphertext in the index") points at the file, not at the
broken pattern `init` itself wrote. Conversely, slash-less patterns anchor
differently in the two dialects (benign only because the filter re-checks the
manifest).
**Direction:** restrict manifest pattern syntax to the common subset (reject
braces at validate time), or attach the filter broadly (`* filter=doctier`) and
let the manifest be the only matcher.

**M2. `doctier gc --trigger <typo>` exits 0 with "nothing to collect".** CONFIRMED (reproduced).
No validation of the trigger value (`cmd/gc.go:25,40`): `--trigger pr_merge`
matches nothing, prints success. A typo'd cron/CI job silently never collects,
forever. **Direction:** whitelist trigger values; unknown → exit 2.

**M3. `check` accepts *any* well-formed age ciphertext — including blobs no current recipient can read.** CONFIRMED (traced).
`ValidCiphertext` proves age-ness, not recipient coverage (`cmd/check.go:82-89`).
Consequences: (a) after hand-editing `recipients.txt` without running `grant`,
or after C2, blobs encrypted to a stale/wrong set pass forever; (b) a writer can
commit a doc encrypted only to themselves and CI is satisfied. The README's
"no AAD" caveat covers substitution, not recipient drift. ssh-ed25519/ssh-rsa
recipient stanzas carry a pubkey-hash tag, so coverage *is* checkable without a
private key. **Direction:** in `check`, verify every recipient in
`recipients.txt` appears in each private blob's header stanzas (warn or fail);
in `grant`, verify decryptability post-renormalize.

**M4. `doctier grant` stages the user's entire dirty worktree.** CONFIRMED (reproduced).
`git add --renormalize .` (`cmd/grant.go:72-78`) is a repo-wide `git add` of
tracked files: unrelated uncommitted modifications get staged alongside the
re-encryption. The suggested "review and commit the changes" then sweeps WIP
into the recipients commit. **Direction:** renormalize only paths matched by
private rules (`git add --renormalize -- <pathspecs>`), or snapshot/restage.

**M5. `pr-merge` and `branch` triggers don't detect merges — they delete anything present on the integration branch.** CONFIRMED (traced).
`gcOnIntegration` (`cmd/gc.go:120-155`): "a doc's branch is considered merged
once the doc is present on the integration branch". A PRD committed *directly*
to main (trunk-based flow) is collected by the very next `git pull`'s post-merge
hook or CI push run, even seconds old. Also `gcBranch` ≡ `gcPRMerge` (same
predicate family, same collection) — `expire.on: pr-merge` and
`expire.on: worktree, scope: branch` are two spellings of identical runtime
behavior, differing only in which `--trigger` flag collects them.
**Direction:** either implement real merge detection (e.g. only collect docs
whose last commit is reachable via a merge commit / `--first-parent` heuristics)
or rename the trigger to what it does; collapse or clearly differentiate the two
spellings.

**M6. Unreachable-rule detection is approximate: partial shadowing passes silently.** CONFIRMED (traced).
`validate` matches the earlier *pattern* against the later pattern *as a literal
path* (`internal/config/config.go:169`). Earlier `**/*.md` before later
`docs/**`: not flagged, yet every `.md` under `docs/` takes the earlier rule —
first-match-wins silently misclassifies a subtree. The check also treats glob
metacharacters in the later pattern as literal path segments (both false
positives and negatives are constructible). **Direction:** document the check as
heuristic; consider flagging *overlap* (both patterns match a common witness)
as a warning instead.

**M7. This repo doesn't follow its own security guidance: no CI runs `doctier check` (or the tests).** CONFIRMED.
`.github/workflows/` contains only `release.yml`. README: "Run `doctier check`
in CI — it is the only host-independent guarantee." The dogfooding repo has no
check job, no test job; releases build from an untested tip. (With C1 unfixed a
check job would also be needed for its own `.harness/**`.)
**Direction:** add a CI workflow running `go build`, `go test`, `doctier check`.

### LOW

**L1. Untracked plaintext at a private path passes full `check` silently** — not in the index, so `cmd/check.go:84` skips it (`continue`). Defensible (nothing committed yet), but a CI check on a repo with a stray decrypted export reports "policy satisfied". A warning would cost nothing.

**L2. `DefaultBranch` guesses "main"** (`internal/gitx/gitx.go:176-186`): with no `origin/HEAD` and a non-main/master integration branch, pr-merge/branch gc skips forever (it does print the skip line). `ephemeral.integration_branch` exists but the failure mode is quiet in CI logs.

**L3. `init` ignores `recipients_file`**: `cmd/init.go:94` hardcodes `.doctier/recipients.txt` and creates it even when the manifest points elsewhere — stray file plus confusion about which file `grant` uses (grant honors the manifest; init doesn't).

**L4. `status` lies about storage for untracked files** — an untracked file shows `git (plaintext)`/`git (encrypted)` though it is in no git storage at all.

**L5. `gc --trigger worktree` only runs `git worktree prune`** (`cmd/gc.go:157-166`) — it never deletes anything itself; the name suggests collection. README's table implies more than it does.

**L6. Merge/rebase of concurrently edited private docs is a dead end**: both sides are (nondeterministic) armor; git's text merge produces interleaved armor + conflict markers. `check` correctly rejects a committed conflict shell, but there is no merge driver (textconv covers diffs only), so the documented flow ends at "pick a side wholesale". Worth a `merge.doctier` driver (decrypt-merge-reencrypt when a key is present, else `ours`/fail).

**L7. Filter is one process per file, whole file in memory** — `runFilter` reloads the manifest and `clean` may spawn `git cat-file` + decrypt per file; no long-running `filter.process` protocol. Fine for docs; will crawl on hundreds of private files (grant renormalizes the world).

**L8. `ensureBlock` duplicates the managed block if markers are corrupted** (end before begin → falls through to append; `cmd/init.go:214-243`). Self-inflicted only.

**L9. Windows binaries are shipped (goreleaser builds windows/amd64+arm64) but hooks are `#!/usr/bin/env sh` scripts and `install.sh` excludes Windows** — untested, undocumented platform; hook behavior under Git-for-Windows sh is assumed, never stated.

**L10. `install.sh` GitHub API parse is fragile** (`grep '"tag_name"'`) and `[ -w /usr/local/bin ] 2>/dev/null` redirect is dead code; both benign today.

**L11. Key handling notes** — decrypted plaintext lands only in the worktree (good: `cachetextconv` deliberately avoided, `cmd/init.go:168-170`); but `LoadIdentity` reads raw passphrase-less private keys with no ssh-agent or age-native-identity support, which *forces* users toward creating passphrase-less keys (README acknowledges; still the weakest link of the whole model).

---

## 3. Design tensions

**T1. The guarantee lives where it doesn't travel.** Filters and hooks are
`.git`-local; the manifest and attributes travel but enforce nothing by
themselves. The design compensates with CI `check` — which is opt-in, absent in
this very repo (M7), and fail-open for quoted paths (C1) and pushed history
(H5). Alternatives: a `pre-receive`-oriented check mode (validate pushed trees,
not the index), a committed hooks runner (lefthook/husky-style) to shrink the
unprotected window, and making `doctier init` part of a documented
clone-bootstrap that also unlocks (H4).

**T2. "Is ciphertext" is conflated with "is correctly encrypted for the current policy".**
The `ValidCiphertext` passthrough is load-bearing in three places (clean guard,
check, grant-renormalize) and is the root cause of C2 and M3, and the reason
`reset --hard` can't recover a worktree (H4). The system needs a notion of
*ciphertext freshness/coverage* — recipient-stanza inspection gives it cheaply
without private keys — and force-re-encrypt must actually re-encrypt.

**T3. Three synced artifacts, two glob dialects, one source of truth.**
`.doctier.yml` → (manual `init` re-run) → `.gitattributes` + `.gitignore`, with
doublestar semantics on one side and gitignore semantics on the other (M1, H3).
Every drift is a policy hole patched only by `check`. Alternative: attach the
filter universally (`* filter=doctier` — clean already passes through
non-private paths) so encryption depends only on the manifest; keep attributes
generation just for `diff=doctier`, and validate the pattern subset.

**T4. Trigger names describe intent, implementations are proxies.**
`pr-merge` = "exists on integration branch" (M5); `worktree` trigger = "prune
bookkeeping" (L5); `branch` scope = pr-merge respelled. The two-axis model is
clean, but the lifetime axis's runtime semantics are looser than the vocabulary
sold to the manifest author, and the gap is where users get surprised
(instant collection on trunk-based flows; scratch that only dies if the
worktree does).

**T5. GC trades safety heuristics against data loss.**
Commit-date-first is right; the mtime fallback plus `os.Remove` for untracked
files (C3) crosses from "collector" into "deleter of things git never had".
A quarantine directory (rename, not unlink) would make every gc action
reversible for the cost of a second sweep.

---

## 4. Expectation gaps

- **Expected** `doctier check` to inspect every staged/tracked file; **found** it
  skips any path git quotes (non-ASCII) — fail-open (C1).
- **Expected** `doctier grant` (revoke flow) to guarantee re-encryption or fail;
  **found** it can pass ciphertext through untouched and still print success (C2).
- **Expected** gc to only ever delete things recoverable from git; **found**
  `os.Remove` of never-committed files on an mtime heuristic, under the default
  trigger (C3).
- **Expected** an invalid glob in the manifest to fail loading; **found** it
  silently matches nothing, downgrading private → public (H1).
- **Expected** `doctier init` to touch only this repo; **found** it can install
  hooks into a global hooks dir, breaking commits in every other repo (H2).
- **Expected** the README clone flow (`clone` → `init`) to yield readable private
  docs; **found** ciphertext with no unlock command and `checkout`/`reset --hard`
  no-ops (H4) — this repo's own `.harness/` demonstrates it.
- **Expected** pre-push to vet what is pushed; **found** it vets the current
  index (H5).
- **Expected** `expire.on: pr-merge` to fire on PR merge; **found** "file exists
  on the integration branch" (M5), and `scope: branch` to be a distinct
  mechanism; **found** the same collection path behind a different flag.
- **Expected** the self-hosting repo to run its advertised CI guarantee; **found**
  release-only CI, no check, no tests (M7).
- **Expected** manifest globs and `.gitattributes` globs to mean the same thing;
  **found** two dialects with silent divergence (M1).

---

## 5. Open questions

1. Is Windows actually supported? Binaries ship for it, but hooks are `sh`
   scripts and nothing is tested or documented (L9).
2. Should `check` gain a `--commits <range>` mode so pre-push/CI can vet history
   rather than one tree (H5)? What is the intended answer for plaintext that is
   already in pushed history — is `git filter-repo` guidance in scope?
3. Is recipient-stanza inspection (pubkey-tag matching) acceptable as the
   mechanism for "encrypted to the current set" (M3), given it is a weak hash
   tag rather than proof of decryptability?
4. What is the intended trunk-based-development story for `pr-merge` ephemerals
   (M5) — is "collected on next push to main" acceptable, or should collection
   require the doc's addition to be reachable only through a merge commit?
5. The manifest is trusted-on-read at filter time: a hostile branch can carry a
   `.doctier.yml` that declassifies paths, and checkout of that branch changes
   policy silently (README's CODEOWNERS note covers commit review, not local
   checkout of unreviewed branches). Is pinning the manifest (e.g. to the
   integration branch's copy) worth the complexity?
6. Should `doctier init` stage/commit what it scaffolds (manifest, recipients,
   attributes)? Today a teammate can run init+grant and forget to commit
   `recipients.txt`, and every other clone's clean then fails closed with a
   confusing "no recipients" error.

---

*Method note: all reproductions used a locally built binary (`go build`, Go 1.24,
age v1.2.1, doublestar v4.7.1) in throwaway repos; the repo itself was not
modified beyond this report.*
