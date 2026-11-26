package browser

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/remote"
)

const connectPort = "9222"

// Connected reports whether werkz is attached to the user's own Chrome (see
// Connect) rather than a browser it started itself.
func (b *Browser) Connected() bool { return b.connected }

// Connect attaches to a Chrome the user started with --remote-debugging-port.
// addr is "9222", "127.0.0.1:9222" or "http://127.0.0.1:9222" (an empty addr
// means 9222). Only loopback addresses are accepted. It opens one new window
// with one blank tab for werkz, in the user's default profile so their
// sign-ins apply, and drives only the tabs werkz opened (and popups they
// open). Every other tab of the user's stays out of reach: see fence.
// AllowHost and ShotDir of o apply; ExecPath, ProfileDir and Headless are
// ignored.
func Connect(ctx context.Context, addr string, o Options) (*Browser, error) {
	hostport, err := parseConnectAddr(addr)
	if err != nil {
		return nil, err
	}
	ws, err := browserWS(ctx, hostport)
	if err != nil {
		return nil, err
	}
	if o.ShotDir == "" {
		cache, _ := os.UserCacheDir()
		if cache == "" {
			return nil, errors.New("cannot pick a screenshot directory: set Options.ShotDir")
		}
		o.ShotDir = filepath.Join(cache, "werkz", "screenshots")
	}
	var kb [8]byte
	_, _ = rand.Read(kb[:])
	b := &Browser{
		opts: o, connected: true, lastShots: map[string]string{}, guardKey: "__wz" + hex.EncodeToString(kb[:]),
		pol: newFencePolicy(), wsURL: ws,
	}
	b.pol.download = b.connectedDownload
	if err := b.attach(ctx); err != nil {
		return nil, fmt.Errorf("%w (to start Chrome for werkz run: %s)", err, LaunchCommand())
	}
	return b, nil
}

// parseConnectAddr turns addr into a loopback host:port, refusing anything
// else without resolving it.
func parseConnectAddr(addr string) (string, error) {
	s := strings.TrimSpace(addr)
	if s == "" {
		s = connectPort
	}
	if n, err := strconv.Atoi(s); err == nil && !strings.ContainsAny(s, "+-") {
		return validHostPort("127.0.0.1", strconv.Itoa(n), addr)
	}
	if i := strings.Index(s, "://"); i >= 0 {
		switch strings.ToLower(s[:i]) {
		case "http", "https", "ws", "wss":
		default:
			return "", fmt.Errorf("%q is not a Chrome debugging address (use a port, host:port or http://host:port)", addr)
		}
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return "", fmt.Errorf("%q is not a Chrome debugging address (use a port, host:port or http://host:port)", addr)
	}
	return validHostPort(host, port, addr)
}

func validHostPort(host, port, orig string) (string, error) {
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return "", fmt.Errorf("%q has no valid port", orig)
	}
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "localhost" {
		h = "127.0.0.1"
	}
	if ip := net.ParseIP(h); ip == nil || !ip.IsLoopback() {
		return "", fmt.Errorf("werkz only connects to a Chrome on this computer (127.0.0.1, localhost or [::1]), not %q", orig)
	}
	return net.JoinHostPort(h, strconv.Itoa(p)), nil
}

// browserWS asks Chrome for the address of its browser-level debugging
// socket. The answer lists no tabs.
func browserWS(ctx context.Context, hostport string) (string, error) {
	fail := func(err error) (string, error) {
		return "", fmt.Errorf("could not reach Chrome at %s: %v. Is it running with --remote-debugging-port? Start it with: %s", hostport, err, LaunchCommand())
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, "http://"+hostport+"/json/version", nil)
	if err != nil {
		return fail(err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return fail(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fail(fmt.Errorf("HTTP %d", resp.StatusCode))
	}
	var v struct {
		WS string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&v); err != nil {
		return fail(errors.New("that port does not speak the Chrome debugging protocol"))
	}
	// Dial the address the user gave, whatever host the answer names.
	i := strings.Index(v.WS, "/devtools/browser/")
	if i < 0 {
		return fail(errors.New("that port does not speak the Chrome debugging protocol"))
	}
	return "ws://" + hostport + v.WS[i:], nil
}

// fenceAllocator hands chromedp a browser reached through a fence.
type fenceAllocator struct {
	f   *fence
	ctx context.Context // ends the browser's message loop
}

func (a *fenceAllocator) Allocate(_ context.Context, opts ...chromedp.BrowserOption) (*chromedp.Browser, error) {
	return chromedp.NewBrowserTransport(a.ctx, a.f, opts...)
}
func (a *fenceAllocator) Wait()          {}
func (a *fenceAllocator) Attaches() bool { return true }

// attach opens werkz's tab in the user's Chrome and prepares it.
func (b *Browser) attach(ctx context.Context) error {
	raw, err := remote.DialContext(ctx, b.wsURL)
	if err != nil {
		return fmt.Errorf("could not connect to Chrome: %w", err)
	}
	f := newFence(b.pol, raw)
	wctx, wcancel := context.WithCancel(context.Background())
	fa := &fenceAllocator{f: f, ctx: wctx}
	allocCtx, allocCancel := chromedp.NewAllocatorContext(context.Background(), fa)
	tab, tabStop := chromedp.NewContext(allocCtx)
	drop := func() {
		allocCancel()
		wcancel()
		_ = f.Close()
	}
	stop := func() { tabStop(); drop() }

	started := make(chan error, 1)
	go func() { started <- chromedp.Do(tab) }() // creates and attaches the tab
	select {
	case err := <-started:
		if err != nil {
			stop()
			return fmt.Errorf("could not open a tab in Chrome: %w", err)
		}
	case <-ctx.Done():
		stop()
		return fmt.Errorf("connecting to Chrome was cancelled: %w", ctx.Err())
	case <-time.After(15 * time.Second):
		stop()
		return errors.New("Chrome did not open a tab within 15s")
	}
	b.initState(drop, tab, tabStop, false)
	if err := b.setup(ctx, tab); err != nil {
		b.shutdown()
		return err
	}
	return nil
}

// startGuardConnected opens the second connection that watches for popups of
// werkz's tab.
func (b *Browser) startGuardConnected(ctx context.Context, tab context.Context) error {
	c := chromedp.FromContext(tab)
	if c == nil || c.Target == nil {
		return errors.New("the browser tab is not attached")
	}
	raw, err := remote.DialContext(ctx, b.wsURL)
	if err != nil {
		return fmt.Errorf("could not connect to Chrome: %w", err)
	}
	g := newGuard(b, newFence(b.pol, raw), c.Target.TargetID)
	b.mu.Lock()
	b.g = g
	b.mu.Unlock()
	if err := g.startConnected(); err != nil {
		return fmt.Errorf("could not watch for popups: %w", err)
	}
	select {
	case <-g.tabReady:
	case <-time.After(3 * time.Second):
	case <-ctx.Done():
	}
	return nil
}

// shutdownConnected closes werkz's own tabs and drops the connections. It
// never closes the browser, and touches no other tab.
func (b *Browser) shutdownConnected() {
	b.mu.Lock()
	tabs, root, alloc, g := b.tabs, b.root, b.alloc, b.g
	b.tab, b.tabStop, b.alloc, b.g = nil, nil, nil, nil
	b.tabs, b.cur, b.root = nil, nil, nil
	b.closed = true
	b.mu.Unlock()
	for _, t := range tabs { // never leave the user's downloads denied
		if !t.gone.Load() {
			rctx, rcancel := context.WithTimeout(t.ctx, 3*time.Second)
			err := b.tabDownloads(rctx, true)
			rcancel()
			if err == nil {
				break
			}
		}
	}
	if g != nil && g.alive() {
		for _, id := range b.pol.tabIDs() {
			_, _ = gcall(g, "", target.CloseTarget, target.CloseTargetParams{TargetID: id})
		}
	}
	if g != nil {
		g.close()
	}
	for _, t := range tabs { // cancels the tab's context; chromedp detaches (the tab is already closed)
		if t != root {
			t.stop()
		}
	}
	if root != nil {
		root.stop()
	}
	if alloc != nil {
		alloc()
	}
}

// LaunchCommand is the command that starts the user's Chrome ready for
// Connect, for this OS. Chrome 136 and later ignore the debugging port on the
// default profile, so it names a dedicated profile directory under the user's
// config directory.
func LaunchCommand() string {
	exe, _ := Find()
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir = os.TempDir()
	}
	return launchCommand(runtime.GOOS, exe, filepath.Join(dir, "werkz", "chrome-connect"))
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func launchCommand(goos, exe, profile string) string {
	port := "--remote-debugging-port=" + connectPort
	switch goos {
	case "windows":
		if exe == "" {
			exe = `C:\Program Files\Google\Chrome\Application\chrome.exe`
		}
		return `"` + exe + `" ` + port + ` "--user-data-dir=` + profile + `"`
	case "darwin":
		app := "Google Chrome"
		if i := strings.Index(exe, ".app/"); i >= 0 {
			app = filepath.Base(exe[:i])
		}
		return `open -na "` + app + `" --args ` + port + ` ` + shellQuote("--user-data-dir="+profile)
	}
	if exe == "" {
		exe = "google-chrome"
	}
	return shellQuote(exe) + " " + port + " " + shellQuote("--user-data-dir="+profile)
}
