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

## apt (Debian, Ubuntu)

```bash
sudo install -d -m 755 /etc/apt/keyrings
curl -fsSL https://tonmoytalukder.github.io/werkz/werkz.gpg | sudo tee /etc/apt/keyrings/werkz.gpg >/dev/null
echo "deb [signed-by=/etc/apt/keyrings/werkz.gpg] https://tonmoytalukder.github.io/werkz/apt stable main" | sudo tee /etc/apt/sources.list.d/werkz.list
sudo apt update
sudo apt install werkz
```

The first three lines add the werkz repository once; after that `sudo apt upgrade` keeps werkz up to date.

## dnf (Fedora, RHEL)

```bash
sudo curl -fsSL https://tonmoytalukder.github.io/werkz/rpm/werkz.repo -o /etc/yum.repos.d/werkz.repo
sudo dnf install werkz
```

`sudo dnf upgrade` then keeps it up to date. Both repositories are signed with the werkz key
(fingerprint `3148 F0F0 B5B3 55FC 9FC1 9B2C 20A3 4C2D 3430 3C54`), served from
[tonmoytalukder.github.io/werkz](https://tonmoytalukder.github.io/werkz/).

## Linux package files

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

werkz checks GitHub for a new release once a day, in the background. What happens next depends on how you
installed it, and werkz always tells you the best way for your installation:

| Installed with | What happens |
|---|---|
| One-line installer or a downloaded file | werkz downloads the new version, verifies it against `SHA256SUMS`, and replaces itself; restart to use it |
| apt (werkz repository) | `sudo apt update && sudo apt install --only-upgrade werkz`; with [automatic updates](#automatic-updates-with-apt) on, Ubuntu installs it for you |
| dnf (werkz repository) | `sudo dnf upgrade werkz` |
| A `.deb`, `.rpm`, `.apk` or Arch package file | werkz shows the command with the download link for the new package |
| Homebrew | `brew upgrade --cask werkz` |
| A folder werkz can't write to | Run the one-line installer again |

While an update is waiting, the status bar shows **↑ werkz X available**, and werkz reminds you when it starts.
Type `/update` to see the exact command (or, for copies that update themselves, to install it right away);
`werkz --update` does the same from a terminal.

- `/update auto off` stops automatic installs; werkz still tells you when a new version is out.
  `/update auto on` turns them back on.
- `WERKZ_NO_UPDATE=1` disables checks for a session. Builds made with `go build` without a version (shown as
  `dev`) never check.
- Only the `werkz` command is replaced; app icons and launchers stay as installed. Running the one-line
  installer again also updates everything and keeps your settings.

### Automatic updates with apt

Ubuntu's background updater installs only Ubuntu's own security updates by default. To let it install new
werkz versions too (usually within a day of a release):

```bash
echo 'Unattended-Upgrade::Origins-Pattern { "origin=werkz"; };' | sudo tee /etc/apt/apt.conf.d/52werkz-unattended
```

Remove that file to turn it off again.

## Uninstalling

- **Linux:** run `uninstall.sh` from the release package, or delete `~/.local/bin/werkz`,
  `~/.local/bin/werkz-launch`, `~/.local/share/applications/werkz.desktop` and the Desktop icon.
- **macOS:** delete `werkz.app` and `~/.local/bin/werkz`.
- **Homebrew:** `brew uninstall --cask werkz`. **Linux packages:** remove the `werkz` package with your package
  manager (for example `sudo apt remove werkz`). To drop the repository too, delete
  `/etc/apt/sources.list.d/werkz.list` and `/etc/apt/keyrings/werkz.gpg`, or `/etc/yum.repos.d/werkz.repo`.

Your settings and API keys are kept in `~/.config/werkz/`; delete that folder to remove them too.
