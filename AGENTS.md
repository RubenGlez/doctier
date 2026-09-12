<!-- doctier:begin -->
## Project context

Managed by doctier — do not edit between the markers.

Entry points (read these first):

- `.harness/engineering/architecture.md`
- `.harness/product/product.md`
- `.harness/product/roadmap.md`

Further docs, by directory:

- `.harness/adr/` (13 docs)
- `.harness/engineering/features/` (3 docs)
- `.harness/product/` (3 docs)
- `.harness/qa/` (2 docs)
<!-- doctier:end -->

## Working in this repo

- This repo self-hosts doctier: its own `.harness/` docs are tracked **encrypted**. Reading them requires an age/SSH key configured as a recipient; without a key they appear as ciphertext.
- Build: `go build ./...`. Test: `go test ./...`.
- Releases are cut by pushing a `v*` tag (goreleaser). The release job runs on a **macOS runner** so the darwin binaries are signed with Apple's real `codesign` and notarized with `notarytool` (see `scripts/macos-sign.sh`). This is deliberate: GoReleaser's built-in quill notarizer signs on Linux, but quill's signature is rejected by Apple Silicon's kernel at exec (`Killed: 9`) even after Apple notarizes it. Signing needs the `MACOS_SIGN_P12` / `MACOS_SIGN_PASSWORD` and `MACOS_NOTARY_*` repo secrets; without them the build falls back to the Go linker's ad-hoc signature (runs locally, not Gatekeeper-trusted).
