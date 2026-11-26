package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"0.2.0", "0.1.0", true}, {"0.1.1", "0.1.0", true}, {"1.0.0", "0.9.9", true},
		{"0.10.0", "0.9.0", true}, {"v0.2.0", "0.1.0", true},
		{"0.1.0", "0.1.0", false}, {"0.1.0", "0.2.0", false},
		{"0.2.0", "dev", false}, {"garbage", "0.1.0", false}, {"0.2.0-rc1", "0.1.0", false},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// fakeReleases serves /latest -> /tag/v<latest> and /download/v<latest>/<files>.
func fakeReleases(t *testing.T, latest string, files map[string][]byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/releases/latest":
			http.Redirect(w, r, "/releases/tag/v"+latest, http.StatusFound)
		case strings.HasPrefix(r.URL.Path, "/releases/tag/"):
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(r.URL.Path, "/releases/download/v"+latest+"/"):
			b, ok := files[filepath.Base(r.URL.Path)]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(b)
		default:
			http.NotFound(w, r)
		}
	}))
}

func release(version string, binary []byte, tamper bool) map[string][]byte {
	u := &Updater{}
	asset := u.Asset(version)
	sum := sha256.Sum256(binary)
	served := binary
	if tamper {
		served = append([]byte{}, binary...)
		served[0] ^= 0xff
	}
	return map[string][]byte{
		asset:        served,
		"SHA256SUMS": []byte(fmt.Sprintf("%s  %s\n%s  werkz-%s-other-arch\n", hex.EncodeToString(sum[:]), asset, strings.Repeat("0", 64), version)),
	}
}

func TestLatestAndInstall(t *testing.T) {
	newBin := []byte("#!/bin/sh\necho new werkz\n")
	srv := fakeReleases(t, "0.2.0", release("0.2.0", newBin, false))
	defer srv.Close()
	dir := t.TempDir()
	exe := filepath.Join(dir, "werkz")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	u := &Updater{Base: srv.URL + "/releases", Exe: exe}
	v, err := u.Latest(context.Background())
	if err != nil || v != "0.2.0" {
		t.Fatalf("Latest = %q, %v", v, err)
	}
	if err := u.Install(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(exe)
	st, _ := os.Stat(exe)
	if string(got) != string(newBin) || st.Mode().Perm()&0o111 == 0 {
		t.Fatalf("binary not replaced correctly: %q mode %v", got, st.Mode())
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".werkz-*")); len(left) != 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
}

func TestTamperedDownloadRefused(t *testing.T) {
	srv := fakeReleases(t, "0.2.0", release("0.2.0", []byte("real binary"), true))
	defer srv.Close()
	exe := filepath.Join(t.TempDir(), "werkz")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := (&Updater{Base: srv.URL + "/releases", Exe: exe}).Install(context.Background(), "0.2.0")
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("tampered download accepted: %v", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "old" {
		t.Fatal("the installed binary was modified despite the failed check")
	}
}

func TestSymlinkedInstallReplacesTarget(t *testing.T) {
	newBin := []byte("new")
	srv := fakeReleases(t, "0.2.0", release("0.2.0", newBin, false))
	defer srv.Close()
	app, bin := t.TempDir(), t.TempDir() // e.g. werkz.app/… and ~/.local/bin
	target := filepath.Join(app, "werkz-real")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(bin, "werkz")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := (&Updater{Base: srv.URL + "/releases", Exe: link}).Install(context.Background(), "0.2.0"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != "new" {
		t.Fatal("the symlink's target wasn't updated")
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink was replaced by a file")
	}
}

func TestReadOnlyInstallFolder(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permissions behave differently here")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "werkz")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(dir, 0o755) }()
	err := (&Updater{Base: "http://127.0.0.1:1", Exe: exe}).Install(context.Background(), "0.2.0")
	if !errors.Is(err, ErrNotWritable) {
		t.Fatalf("want ErrNotWritable, got %v", err)
	}
}

// Package-manager installs are never overwritten: the user is told how to update instead.
func TestPackageManagedInstallsAreLeftAlone(t *testing.T) {
	root := t.TempDir() // no package databases: /usr/bin/werkz from an unknown package manager
	for exe, want := range map[string]string{
		"/usr/bin/werkz": "update it with your package manager",
		"/opt/homebrew/Caskroom/werkz/0.1.1/werkz":      "brew upgrade --cask werkz",
		"/home/linuxbrew/.linuxbrew/Cellar/werkz/x/bin": "brew upgrade werkz",
	} {
		u := &Updater{Base: "http://127.0.0.1:1", Exe: exe, Root: root, RPMQuery: func() bool { return false }}
		var me *ManagedError
		if err := u.Install(context.Background(), "9.9.9"); !errors.As(err, &me) || me.Command != want {
			t.Errorf("%s: got %v, want ManagedError(%q)", exe, err, want)
		}
	}
	if _, ok := (&Updater{Exe: filepath.Join(t.TempDir(), "werkz")}).Managed("9.9.9"); ok {
		t.Error("a user-installed copy must not count as package-managed")
	}
}

// Each install method gets the command that updates it best.
func TestInstallMethods(t *testing.T) {
	write := func(root, p, content string) {
		t.Helper()
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rel := "https://github.com/TonmoyTalukder/werkz/releases/download/v1.2.3/"
	cases := []struct {
		name  string
		files map[string]string
		rpm   bool
		arch  string
		want  Method
	}{
		{"apt repository", map[string]string{"/var/lib/dpkg/info/werkz.list": "", "/etc/apt/sources.list.d/werkz.list": ""}, false, "amd64",
			Method{Kind: "apt", Command: "sudo apt update && sudo apt install --only-upgrade werkz"}},
		{"apt with automatic updates", map[string]string{"/var/lib/dpkg/info/werkz.list": "", "/etc/apt/sources.list.d/werkz.list": "",
			"/etc/apt/apt.conf.d/52werkz-unattended": `Unattended-Upgrade::Origins-Pattern { "origin=werkz"; };`}, false, "amd64",
			Method{Kind: "apt", Command: "sudo apt update && sudo apt install --only-upgrade werkz", Auto: true,
				Note: "your system installs it automatically, usually within a day"}},
		{"downloaded .deb", map[string]string{"/var/lib/dpkg/info/werkz.list": ""}, false, "arm64",
			Method{Kind: "apt", Command: "curl -fsSLO " + rel + "werkz_1.2.3_arm64.deb && sudo apt install ./werkz_1.2.3_arm64.deb",
				Note: "add the werkz apt repository to get updates with apt (tonmoytalukder.github.io/werkz)"}},
		{"dnf repository", map[string]string{"/etc/yum.repos.d/werkz.repo": ""}, true, "amd64",
			Method{Kind: "dnf", Command: "sudo dnf upgrade werkz"}},
		{"downloaded .rpm", nil, true, "arm64",
			Method{Kind: "rpm", Command: "sudo dnf install " + rel + "werkz-1.2.3-1.aarch64.rpm",
				Note: "add the werkz dnf repository to get updates with dnf (tonmoytalukder.github.io/werkz)"}},
		{"pacman", map[string]string{"/var/lib/pacman/local/werkz-1.2.2-1/desc": ""}, false, "amd64",
			Method{Kind: "pacman", Command: "sudo pacman -U " + rel + "werkz-1.2.3-1-x86_64.pkg.tar.zst"}},
		{"apk", map[string]string{"/lib/apk/db/installed": "C:x\nP:busybox\n\nC:y\nP:werkz\nV:1.2.2\n"}, false, "amd64",
			Method{Kind: "apk", Command: "sudo apk add --allow-untrusted " + rel + "werkz_1.2.3_x86_64.apk"}},
	}
	for _, c := range cases {
		root := t.TempDir()
		for p, content := range c.files {
			write(root, p, strings.ReplaceAll(content, "\\n", "\n"))
		}
		rpm := c.rpm
		u := &Updater{Exe: "/usr/bin/werkz", Root: root, GOARCH: c.arch, RPMQuery: func() bool { return rpm }}
		if got := u.Method("1.2.3"); got != c.want {
			t.Errorf("%s:\n got  %+v\n want %+v", c.name, got, c.want)
		}
	}
	if m := (&Updater{Exe: filepath.Join(t.TempDir(), "werkz")}).Method("1.2.3"); m.Kind != "self" || !m.Auto {
		t.Errorf("a copy in a folder werkz can write to updates itself: %+v", m)
	}
}
