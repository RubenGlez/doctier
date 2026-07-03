<!-- doctier:begin -->
## Project context

Managed by doctier — do not edit between the markers.

Read these for project context:

- `.harness/adr/0001-age-in-place-encryption.md`
- `.harness/adr/0002-user-driven-classification.md`
- `.harness/adr/0003-two-axis-visibility-lifetime-model.md`
- `.harness/adr/0004-ephemeral-worktree-scope-only.md`
- `.harness/adr/0005-standalone-cli-own-repo.md`
- `.harness/adr/0006-ephemeral-not-gitignored-sensitive-local.md`
- `.harness/adr/0007-go-single-static-binary.md`
- `.harness/adr/0008-reuse-ssh-keys-as-age-recipients.md`
- `.harness/adr/0009-pr-merge-via-generic-gc.md`
- `.harness/adr/0010-yaml-first-match-manifest.md`
- `.harness/adr/0011-branch-ephemeral-scope.md`
- `.harness/engineering/architecture.md`
- `.harness/engineering/features/branch-ephemeral-scope.md`
- `.harness/product/competitors.md`
- `.harness/product/product.md`
- `.harness/product/roadmap.md`
- `.harness/qa/report.md`
<!-- doctier:end -->

## Working in this repo

- This repo self-hosts doctier: its own `.harness/` docs are tracked **encrypted**. Reading them requires an age/SSH key configured as a recipient; without a key they appear as ciphertext.
- Build: `go build ./...`. Test: `go test ./...`.
- Releases are cut by pushing a `v*` tag (goreleaser). The release job runs on a **macOS runner** so the darwin binaries are signed with Apple's real `codesign` and notarized with `notarytool` (see `scripts/macos-sign.sh`). This is deliberate: GoReleaser's built-in quill notarizer signs on Linux, but quill's signature is rejected by Apple Silicon's kernel at exec (`Killed: 9`) even after Apple notarizes it. Signing needs the `MACOS_SIGN_P12` / `MACOS_SIGN_PASSWORD` and `MACOS_NOTARY_*` repo secrets; without them the build falls back to the Go linker's ad-hoc signature (runs locally, not Gatekeeper-trusted).
