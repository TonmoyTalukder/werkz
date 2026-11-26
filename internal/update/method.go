package update

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Method says how this copy of werkz was installed, and so how it should be updated.
type Method struct {
	Kind string // self, readonly, apt, dnf, rpm, apk, pacman, brew, nix, snap, system
	// Command updates werkz to the given version ("" for self, which werkz installs itself).
	Command string
	// Auto means updates arrive without the user doing anything (werkz's own updater, or a
	// system updater configured for werkz).
	Auto bool
	Note string // extra advice, e.g. how to make updates automatic
}

// InstallScript reinstalls (and so updates) werkz in the user's own folders.
const InstallScript = "curl -fsSL https://raw.githubusercontent.com/TonmoyTalukder/werkz/main/install.sh | sh"

// Method detects how werkz was installed. version is the newest release (used in download
// links for package files that aren't in a repository).
func (u *Updater) Method(version string) Method {
	exe, err := u.target()
	if err != nil {
		return Method{Kind: "readonly", Command: InstallScript}
	}
	switch {
	case strings.Contains(exe, "/Caskroom/"):
		return Method{Kind: "brew", Command: "brew upgrade --cask werkz"}
	case strings.Contains(exe, "/Cellar/") || strings.HasPrefix(exe, "/home/linuxbrew/"):
		return Method{Kind: "brew", Command: "brew upgrade werkz"}
	case strings.HasPrefix(exe, "/nix/store/"):
		return Method{Kind: "nix", Command: "update it with nix"}
	case strings.HasPrefix(exe, "/snap/"):
		return Method{Kind: "snap", Command: "sudo snap refresh werkz", Auto: true}
	case strings.HasPrefix(exe, "/usr/bin/") || strings.HasPrefix(exe, "/usr/sbin/"):
		return u.systemPackage(version)
	}
	if writable(filepath.Dir(exe)) {
		return Method{Kind: "self", Auto: true}
	}
	return Method{Kind: "readonly", Command: InstallScript}
}

// systemPackage finds which Linux package manager installed /usr/bin/werkz.
func (u *Updater) systemPackage(version string) Method {
	arch := u.arch()
	rel := u.base() + "/download/v" + version + "/"
	switch {
	case u.exists("/var/lib/dpkg/info/werkz.list"):
		if !u.exists("/etc/apt/sources.list.d/werkz.list") && !u.exists("/etc/apt/sources.list.d/werkz.sources") {
			deb := fmt.Sprintf("werkz_%s_%s.deb", version, arch)
			return Method{Kind: "apt", Command: "curl -fsSLO " + rel + deb + " && sudo apt install ./" + deb,
				Note: "add the werkz apt repository to get updates with apt (tonmoytalukder.github.io/werkz)"}
		}
		m := Method{Kind: "apt", Command: "sudo apt update && sudo apt install --only-upgrade werkz"}
		if u.unattended() {
			m.Auto, m.Note = true, "your system installs it automatically, usually within a day"
		}
		return m
	case u.glob("/var/lib/pacman/local/werkz-*"):
		pkg := fmt.Sprintf("werkz-%s-1-%s.pkg.tar.zst", version, rpmArch(arch))
		return Method{Kind: "pacman", Command: "sudo pacman -U " + rel + pkg}
	case u.contains("/lib/apk/db/installed", "\nP:werkz\n"):
		apk := fmt.Sprintf("werkz_%s_%s.apk", version, rpmArch(arch))
		return Method{Kind: "apk", Command: "sudo apk add --allow-untrusted " + rel + apk}
	case u.rpmInstalled():
		if u.exists("/etc/yum.repos.d/werkz.repo") {
			return Method{Kind: "dnf", Command: "sudo dnf upgrade werkz"}
		}
		return Method{Kind: "rpm", Command: fmt.Sprintf("sudo dnf install %swerkz-%s-1.%s.rpm", rel, version, rpmArch(arch)),
			Note: "add the werkz dnf repository to get updates with dnf (tonmoytalukder.github.io/werkz)"}
	}
	return Method{Kind: "system", Command: "update it with your package manager"}
}

func (u *Updater) arch() string {
	if u.GOARCH != "" {
		return u.GOARCH
	}
	return runtime.GOARCH
}

func rpmArch(goarch string) string {
	if goarch == "arm64" {
		return "aarch64"
	}
	return "x86_64"
}

// path maps a system path into Root (tests use a fake file system).
func (u *Updater) path(p string) string { return filepath.Join(u.Root, p) }

func (u *Updater) exists(p string) bool {
	_, err := os.Stat(u.path(p))
	return err == nil
}

func (u *Updater) glob(p string) bool {
	m, _ := filepath.Glob(u.path(p))
	return len(m) > 0
}

func (u *Updater) contains(p, s string) bool {
	b, err := os.ReadFile(u.path(p))
	return err == nil && bytes.Contains(append([]byte("\n"), b...), []byte(s))
}

// unattended reports whether the system's automatic updater is set up to install werkz.
func (u *Updater) unattended() bool {
	files, _ := filepath.Glob(u.path("/etc/apt/apt.conf.d/*"))
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil && bytes.Contains(b, []byte("origin=werkz")) {
			return true
		}
	}
	return false
}

func (u *Updater) rpmInstalled() bool {
	if u.RPMQuery != nil {
		return u.RPMQuery()
	}
	if u.Root != "" {
		return false
	}
	p, err := exec.LookPath("rpm")
	if err != nil {
		return false
	}
	return exec.Command(p, "-q", "werkz").Run() == nil
}
