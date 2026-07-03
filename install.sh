#!/bin/sh
# doctier installer — downloads the right prebuilt binary from GitHub Releases.
# No Go toolchain required.
#
#   curl -fsSL https://raw.githubusercontent.com/RubenGlez/doctier/main/install.sh | sh
#
# Env vars:
#   DOCTIER_VERSION   version to install without the leading v (default: latest)
#   DOCTIER_INSTALL   install directory (default: /usr/local/bin, else ~/.local/bin)
set -eu

REPO="RubenGlez/doctier"
BIN="doctier"

fail() {
	echo "install: $1" >&2
	exit 1
}

# --- detect OS/arch, mapped to the release asset naming ---
os=$(uname -s)
case "$os" in
	Linux) os="linux" ;;
	Darwin) os="darwin" ;;
	*) fail "unsupported OS '$os' (use go install, or grab a release manually)" ;;
esac

arch=$(uname -m)
case "$arch" in
	x86_64 | amd64) arch="amd64" ;;
	arm64 | aarch64) arch="arm64" ;;
	*) fail "unsupported architecture '$arch'" ;;
esac

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v tar >/dev/null 2>&1 || fail "tar is required"

# --- resolve version (asset names use the version without the leading v) ---
version="${DOCTIER_VERSION:-}"
if [ -z "$version" ]; then
	tag=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" |
		grep '"tag_name"' | head -n1 | cut -d'"' -f4)
	[ -n "$tag" ] || fail "could not determine the latest release"
	version="${tag#v}"
fi

asset="${BIN}_${version}_${os}_${arch}.tar.gz"
url="https://github.com/${REPO}/releases/download/v${version}/${asset}"

# --- choose an install dir we can actually write to ---
dest="${DOCTIER_INSTALL:-}"
if [ -z "$dest" ]; then
	if [ -w /usr/local/bin ]; then
		dest="/usr/local/bin"
	else
		dest="${HOME}/.local/bin"
	fi
fi
mkdir -p "$dest"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "install: downloading ${BIN} ${version} (${os}/${arch})"
curl -fsSL "$url" -o "${tmp}/${asset}" || fail "download failed: ${url}"
tar -xzf "${tmp}/${asset}" -C "$tmp"
install -m 0755 "${tmp}/${BIN}" "${dest}/${BIN}" 2>/dev/null ||
	{ mv "${tmp}/${BIN}" "${dest}/${BIN}" && chmod 0755 "${dest}/${BIN}"; }

echo "install: installed ${dest}/${BIN}"
case ":${PATH}:" in
	*":${dest}:"*) ;;
	*) echo "install: note — ${dest} is not on your PATH; add it to use '${BIN}' directly" ;;
esac
