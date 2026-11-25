#!/usr/bin/env bash
# Builds release packages into $DIST (default dist/):
#   werkz-<ver>-linux.tar.gz   (amd64 + arm64, launcher, icons, install.sh)
#   werkz-<ver>-macos.zip      (werkz.app with Intel + Apple Silicon binaries)
#   release/                   (the above, bare binaries and SHA256SUMS)
# Usage: packaging/build.sh [version]   (default: the VERSION file)
# GoReleaser runs this as a hook with DIST=build/packages; see .goreleaser.yaml.
set -euo pipefail
cd "$(dirname "$0")/.."
VER="${1:-$(tr -d '[:space:]' < VERSION)}"
VER="${VER#v}"
DIST="${DIST:-dist}"
rm -rf "$DIST" && mkdir -p "$DIST/bin" "$DIST/icons"

echo "→ icons"
cp packaging/icons/* "$DIST/icons/" # regenerate with: python3 packaging/make_icon.py packaging/icons

echo "→ binaries v$VER"
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do
  os=${target%/*}; arch=${target#*/}
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=$VER" -o "$DIST/bin/werkz-$os-$arch" ./cmd/werkz
  echo "   $os/$arch"
done

echo "→ linux package"
L="$DIST/werkz-linux"
mkdir -p "$L/bin" "$L/icons"
cp "$DIST"/bin/werkz-linux-* "$L/bin/"
cp "$DIST"/icons/werkz-{16,24,32,48,64,128,256,512}.png "$L/icons/"
install -m 755 packaging/linux/werkz-launch packaging/linux/install.sh packaging/linux/uninstall.sh "$L/"
install -m 644 LICENSE NOTICE README.md "$L/"
tar -C "$DIST" -czf "$DIST/werkz-$VER-linux.tar.gz" werkz-linux

echo "→ macOS app"
APP="$DIST/werkz.app/Contents"
mkdir -p "$APP/MacOS" "$APP/Resources/bin"
sed "s/__VERSION__/$VER/g" packaging/macos/Info.plist > "$APP/Info.plist"
install -m 755 packaging/macos/launcher.sh "$APP/MacOS/werkz"
install -m 755 "$DIST"/bin/werkz-darwin-* "$APP/Resources/bin/"
cp "$DIST/icons/werkz.icns" "$APP/Resources/werkz.icns"
install -m 644 LICENSE NOTICE "$APP/Resources/"
printf 'APPL????' > "$APP/PkgInfo"
(cd "$DIST" && rm -f "werkz-$VER-macos.zip" && zip -qry "werkz-$VER-macos.zip" werkz.app)

echo "→ release assets"
REL="$DIST/release"
mkdir -p "$REL"
cp "$DIST/werkz-$VER-linux.tar.gz" "$DIST/werkz-$VER-macos.zip" "$REL/"
for f in "$DIST"/bin/werkz-*; do
  install -m 755 "$f" "$REL/werkz-$VER-${f##*/werkz-}"
done
(cd "$REL" && sha256sum werkz-* > SHA256SUMS)

echo "✓ done: $REL"
ls -lh "$REL" | awk 'NR>1 {print "   " $5 "  " $9}'
