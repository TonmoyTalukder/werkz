#!/bin/sh
# werkz installer: downloads a GitHub release, verifies its checksum and installs it.
#
#   curl -fsSL https://raw.githubusercontent.com/TonmoyTalukder/werkz/main/install.sh | sh
#
# Options (environment variables):
#   WERKZ_VERSION=v0.1.0   install this version instead of the latest
#   WERKZ_NO_DESKTOP=1     Linux: install only the command, no app icon
#   WERKZ_BIN_DIR=...      where to put the command (default ~/.local/bin)
set -eu

REPO="TonmoyTalukder/werkz"
BASE="${WERKZ_DOWNLOAD_BASE:-https://github.com/$REPO/releases}" # overridable for testing
BIN_DIR="${WERKZ_BIN_DIR:-$HOME/.local/bin}"

say() { printf '%s\n' "$*"; }
fail() { printf 'werkz installer: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || fail "'$1' is required"; }

need curl
need uname

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) fail "unsupported system $(uname -s); werkz runs on Linux and macOS" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) fail "unsupported CPU $(uname -m)" ;;
esac

# Resolve the version: the "latest" release redirects to .../tag/vX.Y.Z
tag="${WERKZ_VERSION:-}"
if [ -z "$tag" ]; then
  url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$BASE/latest") || fail "can't reach GitHub"
  tag="${url##*/}"
fi
case "$tag" in v*) ;; *) tag="v$tag" ;; esac
ver="${tag#v}"
[ -n "$ver" ] && [ "$ver" != "latest" ] || fail "no release found"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

fetch() { # fetch ASSET: download into $tmp and verify it against SHA256SUMS
  curl -fsSL "$BASE/download/$tag/$1" -o "$tmp/$1" || fail "download failed: $1"
  expected=$(awk -v f="$1" '$2 == f {print $1}' "$tmp/SHA256SUMS")
  [ -n "$expected" ] || fail "no checksum listed for $1"
  if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "$tmp/$1" | awk '{print $1}')
  else
    actual=$(shasum -a 256 "$tmp/$1" | awk '{print $1}')
  fi
  [ "$expected" = "$actual" ] || fail "checksum mismatch for $1 (download corrupted or tampered with)"
}

say "Installing werkz $tag for $os/$arch…"
curl -fsSL "$BASE/download/$tag/SHA256SUMS" -o "$tmp/SHA256SUMS" || fail "can't download release $tag"
mkdir -p "$BIN_DIR"

if [ "$os" = linux ] && [ -z "${WERKZ_NO_DESKTOP:-}" ]; then
  need tar
  fetch "werkz-$ver-linux.tar.gz"
  tar -xzf "$tmp/werkz-$ver-linux.tar.gz" -C "$tmp"
  (cd "$tmp/werkz-linux" && ./install.sh)
elif [ "$os" = darwin ] && [ -z "${WERKZ_NO_DESKTOP:-}" ]; then
  need unzip
  fetch "werkz-$ver-macos.zip"
  unzip -q "$tmp/werkz-$ver-macos.zip" -d "$tmp"
  apps=/Applications
  [ -w "$apps" ] || { apps="$HOME/Applications"; mkdir -p "$apps"; }
  rm -rf "$apps/werkz.app"
  mv "$tmp/werkz.app" "$apps/werkz.app"
  ln -sf "$apps/werkz.app/Contents/Resources/bin/werkz-darwin-$arch" "$BIN_DIR/werkz"
  say "✓ werkz.app installed in $apps"
else
  fetch "werkz-$ver-$os-$arch"
  install -m 755 "$tmp/werkz-$ver-$os-$arch" "$BIN_DIR/werkz"
fi

say "✓ werkz $ver installed: $BIN_DIR/werkz"
[ "$os" = linux ] && [ -z "${WERKZ_NO_DESKTOP:-}" ] && exit 0 # the package installer already gave the PATH hint
case ":$PATH:" in
  *":$BIN_DIR:"*) say "  Run 'werkz' in a project folder to start." ;;
  *) say "  Add $BIN_DIR to your PATH, e.g.: echo 'export PATH=\"$BIN_DIR:\$PATH\"' >> ~/.profile" ;;
esac
