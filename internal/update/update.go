// Package update keeps an installed werkz current: it finds the latest GitHub release,
// downloads the binary for this system, verifies it against the release's SHA256SUMS and
// atomically replaces the running executable. The new version is used from the next start.
package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// DefaultBase is where releases are published.
const DefaultBase = "https://github.com/TonmoyTalukder/werkz/releases"

// maxBinary caps a downloaded binary (a sanity limit, well above the real size).
const maxBinary = 200 << 20

// ErrNotWritable means an update is available but this executable can't be replaced
// (e.g. installed system-wide); the user has to update it themselves.
var ErrNotWritable = errors.New("werkz's install folder isn't writable")

// ManagedError means werkz was installed by a package manager, which owns the file: werkz must
// not replace it, and the user should update through that package manager instead.
type ManagedError struct {
	Command string // how to update, e.g. "brew upgrade --cask werkz"
}

func (e *ManagedError) Error() string {
	return "werkz was installed with a package manager; update it with: " + e.Command
}

// Managed reports whether the executable belongs to a package manager, and how to update it.
func (u *Updater) Managed(version string) (command string, ok bool) {
	switch m := u.Method(version); m.Kind {
	case "self", "readonly":
		return "", false
	default:
		return m.Command, true
	}
}

// Updater checks for and installs releases. The zero value uses the real GitHub releases and
// replaces the running executable.
type Updater struct {
	Base   string       // releases URL (default DefaultBase); overridable for tests
	Client *http.Client // default: an HTTP client with timeouts
	Exe    string       // executable to replace (default: the running one)
	GOOS   string       // default runtime.GOOS
	GOARCH string       // default runtime.GOARCH
	// For tests: a directory standing in for "/" when detecting package managers, and a
	// replacement for asking rpm whether werkz is installed.
	Root     string
	RPMQuery func() bool
}

func (u *Updater) base() string {
	if u.Base != "" {
		return strings.TrimRight(u.Base, "/")
	}
	if b := os.Getenv("WERKZ_UPDATE_BASE"); b != "" { // a mirror, or a fake feed in tests
		return strings.TrimRight(b, "/")
	}
	return DefaultBase
}

func (u *Updater) client() *http.Client {
	if u.Client != nil {
		return u.Client
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

// Latest returns the newest release version (without the "v"), using the "latest" redirect
// (no GitHub API token or rate limit involved).
func (u *Updater) Latest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u.base()+"/latest", nil)
	if err != nil {
		return "", err
	}
	resp, err := u.client().Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	tag := filepath.Base(resp.Request.URL.Path) // …/releases/tag/v1.2.3
	if !strings.HasPrefix(tag, "v") || !valid(tag[1:]) {
		return "", fmt.Errorf("no release found (got %q)", tag)
	}
	return tag[1:], nil
}

// Newer reports whether version a is newer than b (major.minor.patch; a "dev" build is never
// updated, and anything that isn't a plain version is never considered newer).
func Newer(a, b string) bool {
	pa, okA := parse(a)
	pb, okB := parse(b)
	if !okA || !okB {
		return false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func valid(v string) bool { _, ok := parse(v); return ok }

func parse(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// Asset is the release file name of the bare binary for this system.
func (u *Updater) Asset(version string) string {
	goos, goarch := u.GOOS, u.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	return fmt.Sprintf("werkz-%s-%s-%s", version, goos, goarch)
}

// Install downloads version, verifies its checksum and replaces the executable atomically.
func (u *Updater) Install(ctx context.Context, version string) error {
	if cmd, ok := u.Managed(version); ok {
		return &ManagedError{Command: cmd} // never replace a file a package manager owns
	}
	exe, err := u.target()
	if err != nil {
		return err
	}
	dir := filepath.Dir(exe)
	if !writable(dir) {
		return ErrNotWritable
	}
	asset := u.Asset(version)
	sums, err := u.get(ctx, version, "SHA256SUMS", 1<<20)
	if err != nil {
		return fmt.Errorf("downloading checksums: %w", err)
	}
	want, err := checksumFor(sums, asset)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".werkz-update-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	h := sha256.New()
	if err := u.download(ctx, version, asset, io.MultiWriter(tmp, h)); err != nil {
		tmp.Close()
		return fmt.Errorf("downloading %s: %w", asset, err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("checksum mismatch for %s: the download is corrupted or was tampered with", asset)
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), exe) // atomic; the running process keeps its old copy
}

func (u *Updater) target() (string, error) {
	exe := u.Exe
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			return "", err
		}
	}
	// replace the real file, not a symlink to it (e.g. ~/.local/bin/werkz -> werkz.app/...)
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe, nil
}

func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".werkz-write-test-*")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}

func (u *Updater) get(ctx context.Context, version, name string, limit int64) ([]byte, error) {
	var b strings.Builder
	if err := u.download(ctx, version, name, &limitWriter{w: &b, n: limit}); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

func (u *Updater) download(ctx context.Context, version, name string, w io.Writer) error {
	url := fmt.Sprintf("%s/download/v%s/%s", u.base(), version, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := u.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d for %s", resp.StatusCode, name)
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, maxBinary+1))
	if err != nil {
		return err
	}
	if n > maxBinary {
		return fmt.Errorf("%s is larger than expected", name)
	}
	return nil
}

func checksumFor(sums []byte, asset string) (string, error) {
	sc := bufio.NewScanner(strings.NewReader(string(sums)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == asset {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("the release has no checksum for %s", asset)
}

type limitWriter struct {
	w io.Writer
	n int64
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.n {
		return 0, errors.New("response too large")
	}
	l.n -= int64(len(p))
	return l.w.Write(p)
}
