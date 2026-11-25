# Installation

werkz runs on **Linux** (x86-64 and ARM64) and **macOS** (Apple Silicon and Intel).

## One-line installer (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/TonmoyTalukder/werkz/main/install.sh | sh
```

The script downloads the latest release for your system from
[GitHub Releases](https://github.com/TonmoyTalukder/werkz/releases), checks its SHA-256 checksum, and installs:

- **Linux:** the `werkz` command in `~/.local/bin`, plus a **werkz** icon in your app grid and on your
  Desktop. Set `WERKZ_NO_DESKTOP=1` to install only the command.
- **macOS:** **werkz.app** in `/Applications` (or `~/Applications` if that isn't writable), plus a `werkz`
  command in `~/.local/bin`.

To install a specific version: `curl -fsSL …/install.sh | WERKZ_VERSION=v0.1.0 sh`.

If `~/.local/bin` isn't on your `PATH`, the installer tells you; add this to your shell profile:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

## Download a release manually

Every [release](https://github.com/TonmoyTalukder/werkz/releases) has these files:

| File | For |
|---|---|
| `werkz-<version>-linux.tar.gz` | Linux package: both architectures, installer, launcher and icons |
| `werkz-<version>-macos.zip` | macOS: `werkz.app` for Apple Silicon and Intel |
| `werkz-<version>-linux-amd64`, `-linux-arm64`, `-darwin-amd64`, `-darwin-arm64` | The bare command for one system |
| `SHA256SUMS` | Checksums for all of the above |

**Linux package:**

```bash
tar xzf werkz-0.1.0-linux.tar.gz
cd werkz-linux && ./install.sh
```

The installer adds the command, an app-grid entry and a Desktop icon. If the Desktop icon shows as a plain
file, right-click it and choose **Allow Launching**. Clicking it opens werkz in a new terminal window
(ptyxis, GNOME Terminal, Konsole, kitty, Alacritty, WezTerm, foot, Tilix or xterm, whichever is installed);
set `WERKZ_TERMINAL=kitty` to choose one.

**macOS app:** unzip `werkz-0.1.0-macos.zip` and drag **werkz.app** to Applications. The app isn't signed with
an Apple Developer ID, so the first time you open a *browser-downloaded* copy, right-click it and choose
**Open**, or run:

```bash
xattr -dr com.apple.quarantine /Applications/werkz.app
```

macOS also asks once for permission for werkz to control Terminal. (The one-line installer downloads with
`curl`, which doesn't set the quarantine flag.)

**A bare binary:** download the one for your system, then:

```bash
chmod +x werkz-0.1.0-linux-amd64
mv werkz-0.1.0-linux-amd64 ~/.local/bin/werkz
```

Verify any download with `sha256sum -c SHA256SUMS --ignore-missing` (Linux) or
`shasum -a 256 -c SHA256SUMS --ignore-missing` (macOS).

## Homebrew (macOS and Linux)

```bash
brew install --cask ZenixBit/tap/werkz
```

This installs the `werkz` command (no app icon). Update with `brew upgrade --cask werkz`.

## Linux packages

Each release has `.deb` (Debian, Ubuntu), `.rpm` (Fedora, RHEL, openSUSE), `.apk` (Alpine) and Arch Linux
(`.pkg.tar.zst`) packages for x86-64 and ARM64. They install `werkz` in `/usr/bin` with an app-grid entry and
icons. Download the one for your system from the [releases page](https://github.com/TonmoyTalukder/werkz/releases/latest), then:

```bash
sudo apt install ./werkz_0.2.0_amd64.deb        # Debian, Ubuntu
sudo dnf install ./werkz-0.2.0-1.x86_64.rpm     # Fedora, RHEL
sudo pacman -U ./werkz-0.2.0-1-x86_64.pkg.tar.zst  # Arch
sudo apk add --allow-untrusted ./werkz_0.2.0_x86_64.apk  # Alpine
```

Update by installing the newer package the same way.

## With Go

```bash
go install github.com/TonmoyTalukder/werkz/cmd/werkz@latest
```

This needs Go 1.26 or later and puts `werkz` in `~/go/bin`. It installs the command only (no app icon).

## From source

```bash
git clone https://github.com/TonmoyTalukder/werkz.git
cd werkz
go build -o werkz ./cmd/werkz
./werkz
```

To build the Linux and macOS packages yourself, see [Development](development.md).

## Updating

werkz **updates itself**. Once a day, in the background, it checks GitHub for a newer release. If there is
one, it downloads the binary for your system, verifies it against the release's `SHA256SUMS`, and replaces
itself. The new version is used the next time you start werkz, and the status bar says when a restart is
needed.

- `/update` checks and installs right away; `werkz --update` does the same from a terminal.
- `/update auto off` turns automatic updates off (`/update auto on` turns them back on); setting
  `WERKZ_NO_UPDATE=1` disables them for a session.
- If werkz is installed somewhere it can't write to (for example system-wide), it tells you an update is
  available instead; run the one-line installer again to update.
- Builds made with `go build` without a version (shown as `dev`) never update themselves.
- **Homebrew and Linux packages** (`.deb`, `.rpm`, `.apk`, Arch) are owned by the package manager, so werkz
  never replaces them. It tells you a new version is out and how to update instead, for example
  `brew upgrade --cask werkz`.

Only the `werkz` command is replaced; app icons and launchers stay as installed. Running the one-line installer
again also updates everything and keeps your settings.

## Uninstalling

- **Linux:** run `uninstall.sh` from the release package, or delete `~/.local/bin/werkz`,
  `~/.local/bin/werkz-launch`, `~/.local/share/applications/werkz.desktop` and the Desktop icon.
- **macOS:** delete `werkz.app` and `~/.local/bin/werkz`.
- **Homebrew:** `brew uninstall --cask werkz`. **Linux packages:** remove the `werkz` package with your package
  manager (for example `sudo apt remove werkz`).

Your settings and API keys are kept in `~/.config/werkz/`; delete that folder to remove them too.
