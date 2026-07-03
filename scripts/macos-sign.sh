#!/usr/bin/env bash
# Sign (and, when notary credentials are present, notarize) a single macOS binary
# with a real Developer ID certificate using Apple's own codesign + notarytool.
#
# Why this exists: GoReleaser's built-in `notarize` block signs with quill (pure
# Go, runs on Linux). Quill's signature passes Apple's notary service but is
# REJECTED by Apple Silicon's kernel at exec ("does not satisfy its designated
# Requirement" -> Killed: 9). Signing with Apple's real codesign on a macOS runner
# produces a kernel-valid signature. Invoked from GoReleaser's build post-hook as:
#   ./scripts/macos-sign.sh "<binary path>" "<goos>"
#
# No-op for non-darwin targets and for local snapshot builds where no signing
# identity is configured (the Go linker's ad-hoc signature already makes a
# macOS-native build runnable, just not Gatekeeper-trusted when quarantined).
set -euo pipefail

BIN="${1:?usage: macos-sign.sh <binary> <goos>}"
GOOS="${2:?usage: macos-sign.sh <binary> <goos>}"

[ "$GOOS" = "darwin" ] || exit 0

if [ -z "${MACOS_SIGN_IDENTITY:-}" ]; then
	echo "macos-sign: no MACOS_SIGN_IDENTITY set — leaving $BIN with its linker ad-hoc signature"
	exit 0
fi

echo "macos-sign: codesigning $BIN as '$MACOS_SIGN_IDENTITY'"
# --options runtime enables the hardened runtime (required for notarization);
# --timestamp fetches a trusted timestamp so the signature stays valid after the
# cert expires.
codesign --force --timestamp --options runtime --sign "$MACOS_SIGN_IDENTITY" "$BIN"
codesign --verify --strict --verbose=2 "$BIN"

# Notarization is optional: it needs the App Store Connect API key trio. A bare
# CLI binary cannot be stapled (stapling only works on .app/.dmg/.pkg), so we
# submit a zip and rely on Gatekeeper's online ticket check at first run.
if [ -n "${MACOS_NOTARY_KEY:-}" ] && [ -n "${MACOS_NOTARY_KEY_ID:-}" ] && [ -n "${MACOS_NOTARY_ISSUER_ID:-}" ]; then
	workdir="$(mktemp -d)"
	keyfile="$workdir/AuthKey.p8"
	# The secret may hold the raw .p8 PEM or a base64 blob; accept either.
	if printf '%s' "$MACOS_NOTARY_KEY" | grep -q "BEGIN PRIVATE KEY"; then
		printf '%s' "$MACOS_NOTARY_KEY" >"$keyfile"
	else
		printf '%s' "$MACOS_NOTARY_KEY" | base64 --decode >"$keyfile"
	fi
	zip="$workdir/$(basename "$BIN").zip"
	ditto -c -k --keepParent "$BIN" "$zip"
	echo "macos-sign: notarizing $BIN (waiting for Apple)…"
	xcrun notarytool submit "$zip" \
		--key "$keyfile" \
		--key-id "$MACOS_NOTARY_KEY_ID" \
		--issuer "$MACOS_NOTARY_ISSUER_ID" \
		--wait
	rm -rf "$workdir"
	echo "macos-sign: notarized $BIN"
else
	echo "macos-sign: notary credentials absent — $BIN signed but not notarized"
fi
