// Package browser drives the user's own installed Chromium-family browser so
// werkz can check its frontend changes on localhost dev servers the way a
// developer would: open the page, click, type, read the console, take a
// screenshot. It never downloads a browser and never touches the user's
// profile.
package browser

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/remote"
)

const (
	opTimeout   = 20 * time.Second // default budget of one operation
	ringSize    = 500              // console entries and requests kept
	maxBlocked  = 50               // blocked-action notes kept between snapshots
	maxDLSeen   = 200              // download GUIDs remembered
	maxTreeSnap = 12000            // Snapshot.Tree cap, in characters
)

var browserNames = []string{
	"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
	"microsoft-edge", "microsoft-edge-stable", "brave-browser",
}

var macApps = []string{
	"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	"/Applications/Chromium.app/Contents/MacOS/Chromium",
	"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
}

// Find returns the path of an installed Chromium-family browser. werkz never
// downloads one, so the error says how to get one.
func Find() (string, error) {
	if p := strings.TrimSpace(os.Getenv("WERKZ_BROWSER")); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		if lp, err := exec.LookPath(p); err == nil {
			return lp, nil
		}
		return "", fmt.Errorf("WERKZ_BROWSER is set to %q but that is not an executable file", p)
	}
	for _, n := range browserNames {
		if p, err := exec.LookPath(n); err == nil {
			return p, nil
		}
	}
	for _, p := range macApps {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", errors.New("no browser found: werkz never downloads one. Install Google Chrome, Chromium or Microsoft Edge, " +
		"or set WERKZ_BROWSER to the path of a Chromium-based browser")
}

// Options configure Start.
type Options struct {
	ExecPath   string // "" = Find()
	ProfileDir string // "" = <os.UserCacheDir()>/werkz/browser-profile (a separate werkz profile, never the user's)
	Headless   bool
	ShotDir    string // "" = <os.UserCacheDir()>/werkz/screenshots
	// AllowHost vets navigations the page starts itself (links, redirects,
	// JS, popups, iframes): hosts it rejects are blocked. The host is the
	// URL's host including any port ("localhost:3000"), lower-cased, with the
	// scheme's default port (80, 443) and a trailing dot dropped. nil = allow
	// all. Navigate() is pre-approved by the caller for the host:port it opens.
	AllowHost func(host string) bool
}

// Browser is one werkz-owned browser with a single reused tab.
type Browser struct {
	opts Options

	opMu sync.Mutex // serializes operations on the tab

	mu       sync.Mutex // guards everything below; event handlers take it briefly
	headless bool
	alloc    context.CancelFunc
	tab      context.Context
	tabStop  context.CancelFunc
	mainID   cdp.FrameID

	refs    map[string]refInfo
	refNode map[cdp.BackendNodeID]string

	console    *ring[ConsoleEntry]
	reqs       map[network.RequestID]*Request
	reqOrder   *ring[network.RequestID]
	reqStart   map[network.RequestID]float64 // live requests; start time in seconds
	errTotal   int                           // cumulative console errors / failed requests, for Snapshot deltas
	failTotal  int
	errMark    int
	failMark   int
	inflight   int
	lastNet    time.Time
	loading    bool
	mainStatus int
	lastURL    string
	mainDocID  network.RequestID
	blocked    []string
	downloads  []string
	dlSeen     map[string]bool
	dlOrder    []string
	dlPending  map[string]dlInfo // guid -> file, for allowed downloads
	dlDir      string            // download directory of the running action ("" = denied)
	approved   string            // host:port approved by the current Navigate call
	onceOnly   bool              // approved is for the first main-frame request only (history)
	armed      bool              // the running action may submit forms
	popups     int               // popups being vetted
	frameSeen  map[string]bool   // hosts of blocked iframes already reported
	lastShots  map[string]string
	closed     bool

	guardKey string
	g        *guard
	profile  string // profile directory in use
	tempProf bool   // profile is a per-process fallback to remove on Close
}

type dlInfo struct{ name, dir string }

// Start launches the browser and opens the single werkz tab.
func Start(ctx context.Context, o Options) (*Browser, error) {
	if o.ExecPath == "" {
		p, err := Find()
		if err != nil {
			return nil, err
		}
		o.ExecPath = p
	}
	cache, _ := os.UserCacheDir()
	if o.ProfileDir == "" {
		if cache == "" {
			return nil, errors.New("cannot pick a browser profile directory: set Options.ProfileDir")
		}
		o.ProfileDir = filepath.Join(cache, "werkz", "browser-profile")
	}
	if o.ShotDir == "" {
		if cache == "" {
			return nil, errors.New("cannot pick a screenshot directory: set Options.ShotDir")
		}
		o.ShotDir = filepath.Join(cache, "werkz", "screenshots")
	}
	var kb [8]byte
	_, _ = rand.Read(kb[:])
	b := &Browser{opts: o, headless: o.Headless, lastShots: map[string]string{}, guardKey: "__wz" + hex.EncodeToString(kb[:])}
	if err := os.MkdirAll(filepath.Dir(o.ProfileDir), 0o700); err == nil {
		b.profile, b.tempProf = pickProfile(o.ProfileDir)
	} else {
		b.profile = o.ProfileDir
	}
	if err := b.launch(ctx, o.Headless); err != nil {
		b.removeTempProfile()
		return nil, err
	}
	return b, nil
}

// launch starts a browser process and prepares the tab. Callers hold no locks.
func (b *Browser) launch(ctx context.Context, headless bool) error {
	if err := os.MkdirAll(b.profile, 0o700); err != nil {
		return fmt.Errorf("cannot create the browser profile directory: %w", err)
	}
	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	if headless {
		opts = append(opts, chromedp.Flag("headless", "new"))
	} else {
		opts = append(opts, chromedp.VisibleWindow, chromedp.Flag("start-maximized", false))
	}
	opts = append(opts,
		chromedp.ExecPath(b.opts.ExecPath),
		chromedp.UserDataDir(b.profile),
		remote.WebSocket, // the guard connection needs a debugging port
		chromedp.WindowSize(1280, 800),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("password-store", "basic"),
		chromedp.Flag("disable-features", "Translate,AutofillServerCommunication,PasswordManagerOnboarding"),
		chromedp.Flag("enable-automation", headless),
	)
	_ = os.Remove(filepath.Join(b.profile, "DevToolsActivePort"))
	// The browser must outlive the individual tool calls, so it hangs off
	// Background rather than ctx.
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	tab, tabStop := chromedp.NewContext(allocCtx)

	stop := func() { tabStop(); allocCancel() }
	started := make(chan error, 1)
	go func() { started <- chromedp.Do(tab) }() // first Run starts Chrome; it must not carry a timeout
	select {
	case err := <-started:
		if err != nil {
			stop()
			return fmt.Errorf("could not start the browser (%s): %w", b.opts.ExecPath, err)
		}
	case <-ctx.Done():
		stop()
		return fmt.Errorf("browser start was cancelled: %w", ctx.Err())
	case <-time.After(30 * time.Second):
		stop()
		return errors.New("the browser did not start within 30s")
	}

	b.mu.Lock()
	b.alloc, b.tab, b.tabStop = allocCancel, tab, tabStop
	b.headless = headless
	b.refs, b.refNode = map[string]refInfo{}, map[cdp.BackendNodeID]string{}
	b.console, b.reqs, b.reqOrder = newRing[ConsoleEntry](ringSize), map[network.RequestID]*Request{}, newRing[network.RequestID](ringSize)
	b.reqStart = map[network.RequestID]float64{}
	b.errTotal, b.failTotal, b.errMark, b.failMark = 0, 0, 0, 0
	b.inflight, b.loading, b.mainStatus = 0, false, 0
	b.blocked, b.downloads = nil, nil
	b.dlSeen, b.dlOrder, b.dlPending, b.dlDir = map[string]bool{}, nil, map[string]dlInfo{}, ""
	b.frameSeen, b.popups, b.armed = map[string]bool{}, 0, false
	b.approved, b.onceOnly = "", false
	b.closed = false
	b.mu.Unlock()

	if err := b.setup(ctx, tab); err != nil {
		b.shutdown()
		return err
	}
	return nil
}

// setup subscribes to events before enabling the domains so none are missed.
func (b *Browser) setup(ctx context.Context, tab context.Context) error {
	if err := chromedp.Listen(tab, b.onEvent); err != nil {
		return err
	}
	if err := chromedp.ListenBrowser(tab, b.onEvent); err != nil {
		return err
	}
	octx, cancel := b.opContext(ctx, tab)
	defer cancel()
	steps := []func() error{
		func() error { _, err := chromedp.Call(octx, page.Enable, page.EnableParams{}); return err },
		func() error { _, err := chromedp.Call(octx, network.Enable, network.EnableParams{}); return err },
		func() error { _, err := chromedp.Call(octx, runtime.Enable, cdp.Empty{}); return err },
		func() error { _, err := chromedp.Call(octx, log.Enable, cdp.Empty{}); return err },
		func() error {
			_, err := chromedp.Call(octx, fetch.Enable, fetch.EnableParams{
				Patterns: []*fetch.RequestPattern{{ResourceType: network.ResourceTypeDocument, RequestStage: fetch.RequestStageRequest}},
			})
			return err
		},
		func() error { return b.setDownloads(octx, "") },
		func() error { return b.installSubmitGuard(octx) },
		func() error { return b.startGuard(octx, tab) },
		func() error {
			ft, err := chromedp.Call(octx, page.GetFrameTree, cdp.Empty{})
			if err == nil && ft.FrameTree != nil && ft.FrameTree.Frame != nil {
				b.mu.Lock()
				b.mainID = ft.FrameTree.Frame.ID
				b.mu.Unlock()
			}
			return err
		},
	}
	for _, s := range steps {
		if err := s(); err != nil {
			return fmt.Errorf("could not set up the browser tab: %w", err)
		}
	}
	return nil
}

func (b *Browser) setDownloads(ctx context.Context, dir string) error {
	p := browser.SetDownloadBehaviorParams{Behavior: browser.SetDownloadBehaviorBehaviorDeny, EventsEnabled: new(true)}
	if dir != "" {
		p.Behavior = browser.SetDownloadBehaviorBehaviorAllow
		p.DownloadPath = dir
	}
	_, err := chromedp.CallBrowser(ctx, browser.SetDownloadBehavior, p)
	return err
}

// opContext bounds one operation: the default timeout, cut short when the
// caller's context ends. It hangs off the tab so the browser survives it.
func (b *Browser) opContext(call, tab context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(tab, opTimeout)
	stop := context.AfterFunc(call, cancel)
	return ctx, func() { stop(); cancel() }
}

// begin serializes operations and returns the tab context for one of them.
func (b *Browser) begin(call context.Context) (context.Context, func(), error) {
	b.opMu.Lock()
	b.mu.Lock()
	tab, closed := b.tab, b.closed
	b.mu.Unlock()
	if closed || tab == nil {
		b.opMu.Unlock()
		return nil, nil, errors.New("the browser is closed")
	}
	if err := call.Err(); err != nil {
		b.opMu.Unlock()
		return nil, nil, err
	}
	ctx, cancel := b.opContext(call, tab)
	return ctx, func() { cancel(); b.opMu.Unlock() }, nil
}

// startGuard connects the raw guard connection that pauses popups and
// iframes before they load.
func (b *Browser) startGuard(ctx context.Context, tab context.Context) error {
	c := chromedp.FromContext(tab)
	if c == nil || c.Target == nil {
		return errors.New("the browser tab is not attached")
	}
	ws, err := readActivePort(b.profile)
	if err != nil {
		return err
	}
	g, err := dialGuard(ctx, b, ws, c.Target.TargetID)
	if err != nil {
		return fmt.Errorf("could not connect to the browser: %w", err)
	}
	b.mu.Lock()
	b.g = g
	b.mu.Unlock()
	if err := g.start(); err != nil {
		return fmt.Errorf("could not watch for popups: %w", err)
	}
	// The tab is attached asynchronously; wait so the first navigation is
	// already watched.
	select {
	case <-g.tabReady:
	case <-time.After(3 * time.Second):
	case <-ctx.Done():
	}
	return nil
}

// shutdown stops the browser process and waits for it, so the profile lock is
// released before a relaunch.
func (b *Browser) shutdown() {
	b.mu.Lock()
	tab, stop, alloc, g := b.tab, b.tabStop, b.alloc, b.g
	b.tab, b.tabStop, b.alloc, b.g = nil, nil, nil, nil
	b.closed = true
	b.mu.Unlock()
	if g != nil {
		g.close()
	}
	if tab != nil {
		_ = chromedp.Cancel(tab)
	}
	if stop != nil {
		stop()
	}
	if alloc != nil {
		alloc()
	}
}

func (b *Browser) removeTempProfile() {
	if b.tempProf && b.profile != "" {
		_ = os.RemoveAll(b.profile)
	}
}

// Close shuts the browser down and removes a fallback profile directory.
func (b *Browser) Close() error {
	b.shutdown()
	b.removeTempProfile()
	return nil
}

// Alive reports whether the browser can still be driven: not closed, the tab
// and the browser process are up. When it is false, call SetHeadless with
// the current mode (or Start a new Browser) to bring it back.
func (b *Browser) Alive() bool {
	b.mu.Lock()
	tab, closed, g := b.tab, b.closed, b.g
	b.mu.Unlock()
	if closed || tab == nil || tab.Err() != nil || g == nil || !g.alive() {
		return false
	}
	c := chromedp.FromContext(tab)
	if c == nil || c.Browser == nil {
		return false
	}
	select {
	case <-c.Browser.LostConnection:
		return false
	default:
	}
	return true
}

// Headless reports whether the browser runs without a window.
func (b *Browser) Headless() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.headless
}

// SetHeadless relaunches the browser in the other mode and reopens the
// current URL. Page state (form input, scroll) is lost. If the relaunch
// fails the Browser is left closed (Alive() == false) and the error says so.
// Called with the current mode on a dead browser, it restarts it.
func (b *Browser) SetHeadless(ctx context.Context, headless bool) error {
	b.opMu.Lock()
	defer b.opMu.Unlock()
	b.mu.Lock()
	cur, headlessNow := b.currentURLLocked(), b.headless
	b.mu.Unlock()
	if headlessNow == headless && b.Alive() {
		return nil
	}
	b.shutdown()
	if err := b.launch(ctx, headless); err != nil {
		b.shutdown() // never leave a half-open browser
		return fmt.Errorf("the browser could not be relaunched and is now closed: %w", err)
	}
	if cur == "" || cur == "about:blank" {
		return nil
	}
	_, err := b.navigateLocked(ctx, cur)
	return err
}

// URL returns the address of the current page ("" before the first
// navigation).
func (b *Browser) URL() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.currentURLLocked()
}

func (b *Browser) currentURLLocked() string { return b.lastURL }

// hostOf returns the URL's host as AllowHost and the approval logic see it:
// lower-cased "host:port" with the scheme's default port and a trailing dot
// dropped.
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return hostKey(u)
}

func hostKey(u *url.URL) string {
	name := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if strings.Contains(name, ":") {
		name = "[" + name + "]"
	}
	port := u.Port()
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	if port == "" {
		return name
	}
	return name + ":" + port
}
