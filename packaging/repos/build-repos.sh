#!/usr/bin/env bash
# Builds the signed APT and RPM repositories served from GitHub Pages
# (https://tonmoytalukder.github.io/werkz/) from a release's packages.
#
# Usage: build-repos.sh DIST_DIR SITE_DIR
#   DIST_DIR  folder with the release's .deb and .rpm files (GoReleaser's dist/)
#   SITE_DIR  checkout of the gh-pages branch; apt/ and rpm/ are replaced
# Needs apt-ftparchive (apt-utils), createrepo_c and gpg with the signing key imported;
# SIGNING_KEY_ID selects the key. Only the latest release is kept: apt and dnf just need
# the newest version, and it keeps the Pages site small.
set -euo pipefail
DIST="$(cd "$1" && pwd)"
SITE="$(cd "$2" && pwd)"
KEY="${SIGNING_KEY_ID:?set SIGNING_KEY_ID to the fingerprint of the signing key}"
sign() { gpg --batch --yes --local-user "$KEY" "$@"; }

echo "→ apt"
rm -rf "$SITE/apt"
mkdir -p "$SITE/apt/pool/main"
cp "$DIST"/*.deb "$SITE/apt/pool/main/"
(
  cd "$SITE/apt"
  for arch in amd64 arm64; do
    d="dists/stable/main/binary-$arch"
    mkdir -p "$d"
    apt-ftparchive --arch "$arch" packages pool > "$d/Packages"
    gzip -9kn "$d/Packages"
  done
  apt-ftparchive \
    -o APT::FTPArchive::Release::Origin=werkz \
    -o APT::FTPArchive::Release::Label=werkz \
    -o APT::FTPArchive::Release::Suite=stable \
    -o APT::FTPArchive::Release::Codename=stable \
    -o APT::FTPArchive::Release::Architectures="amd64 arm64" \
    -o APT::FTPArchive::Release::Components=main \
    -o APT::FTPArchive::Release::Description="werkz packages" \
    release dists/stable > Release.tmp
  mv Release.tmp dists/stable/Release
  sign --clearsign -o dists/stable/InRelease dists/stable/Release
  sign --armor --detach-sign -o dists/stable/Release.gpg dists/stable/Release
)

echo "→ rpm"
rm -rf "$SITE/rpm"
mkdir -p "$SITE/rpm"
cp "$DIST"/*.rpm "$SITE/rpm/"
createrepo_c --quiet "$SITE/rpm"
sign --armor --detach-sign -o "$SITE/rpm/repodata/repomd.xml.asc" "$SITE/rpm/repodata/repomd.xml"
cat > "$SITE/rpm/werkz.repo" <<'REPO'
[werkz]
name=werkz
baseurl=https://tonmoytalukder.github.io/werkz/rpm
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=https://tonmoytalukder.github.io/werkz/werkz.asc
REPO

touch "$SITE/.nojekyll"
echo "✓ repositories in $SITE"
