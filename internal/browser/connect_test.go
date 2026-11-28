package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/chromedp/cdproto"
	"github.com/chromedp/cdproto/cdp/jsonv2"
	rt "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/remote"
)

func TestConnectAddr(t *testing.T) {
	for in, want := range map[string]string{
		"9222":                    "127.0.0.1:9222",
		"":                        "127.0.0.1:9222",
		"127.0.0.1:9333":          "127.0.0.1:9333",
		"localhost:9222":          "127.0.0.1:9222",
		"http://127.0.0.1:9222":   "127.0.0.1:9222",
		"http://127.0.0.1:9222/":  "127.0.0.1:9222",
		"ws://localhost:9222/dev": "127.0.0.1:9222",
		"[::1]:9222":              "[::1]:9222",
		"http://[::1]:9222":       "[::1]:9222",
		"127.1.2.3:80":            "127.1.2.3:80",
	} {
		if got, err := parseConnectAddr(in); err != nil || got != want {
			t.Errorf("parseConnectAddr(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"10.0.0.5:9222", "192.168.1.2:9222", "http://example.com:9222", "example.com:9222", "127.0.0.1.evil.com:9222",
		"user@127.0.0.1:9222", "0.0.0.0:9222", "http://8.8.8.8:9222", "[::2]:9222", "localhost", "127.0.0.1:0",
		"127.0.0.1:99999", "ftp://127.0.0.1:9222", "http://evil.com/@127.0.0.1:9222", "-1", "0", "70000",
	} {
		if got, err := parseConnectAddr(in); err == nil {
			t.Errorf("parseConnectAddr(%q) = %q, want an error", in, got)
		}
	}
}

func TestConnectRefusesNonLoopback(t *testing.T) {
	_, err := Connect(context.Background(), "http://example.com:9222", Options{})
	if err == nil || !strings.Contains(err.Error(), "only connects to a Chrome on this computer") {
		t.Fatalf("non-loopback: %v", err)
	}
}

func TestConnectDeadPort(t *testing.T) {
	u, _ := url.Parse(deadURL(t))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := Connect(ctx, u.Port(), Options{})
	if err == nil {
		t.Fatal("connected to a dead port")
	}
	if !strings.Contains(err.Error(), "could not reach Chrome") {
		t.Errorf("error: %v", err)
	}
}

func TestLaunchCommand(t *testing.T) {
	linux := launchCommand("linux", "/usr/bin/google-chrome", "/home/me/.config/werkz/chrome-connect")
	if linux != "/usr/bin/google-chrome --remote-debugging-port=9222 --user-data-dir=/home/me/.config/werkz/chrome-connect" {
		t.Errorf("linux: %s", linux)
	}
	spaced := launchCommand("linux", "/opt/my chrome/chrome", "/home/o'x/chrome-connect")
	if !strings.HasPrefix(spaced, "'/opt/my chrome/chrome' ") || !strings.Contains(spaced, `'--user-data-dir=/home/o'\''x/chrome-connect'`) {
		t.Errorf("quoting: %s", spaced)
	}
	mac := launchCommand("darwin", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "/Users/me/Library/Application Support/werkz/chrome-connect")
	if mac != `open -na "Google Chrome" --args --remote-debugging-port=9222 '--user-data-dir=/Users/me/Library/Application Support/werkz/chrome-connect'` {
		t.Errorf("mac: %s", mac)
	}
	win := launchCommand("windows", "", `C:\Users\me\AppData\Roaming\werkz\chrome-connect`)
	if win != `& "C:\Program Files\Google\Chrome\Application\chrome.exe" --remote-debugging-port=9222 "--user-data-dir=C:\Users\me\AppData\Roaming\werkz\chrome-connect"` {
		t.Errorf("windows: %s", win)
	}
	if cmd := LaunchCommand(); !strings.Contains(cmd, "--remote-debugging-port=9222") || !strings.Contains(cmd, filepath.Join("werkz", "chrome-connect")) {
		t.Errorf("LaunchCommand: %s", cmd)
	}
}

// fakeConn is a browser the fence talks to in unit tests.
type fakeConn struct {
	in   chan *cdproto.Message
	mu   sync.Mutex
	sent []*cdproto.Message
}

func newFakeConn() *fakeConn { return &fakeConn{in: make(chan *cdproto.Message, 64)} }
func (c *fakeConn) Read(ctx context.Context, m *cdproto.Message) error {
	select {
	case x, ok := <-c.in:
		if !ok {
			return context.Canceled
		}
		*m = *x
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (c *fakeConn) Write(_ context.Context, m *cdproto.Message) error {
	c.mu.Lock()
	c.sent = append(c.sent, m)
	c.mu.Unlock()
	return nil
}
func (c *fakeConn) Close() error { return nil }
func (c *fakeConn) methods() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, m := range c.sent {
		out = append(out, string(m.Method))
	}
	return out
}

func msg(id int64, sess, method, params string) *cdproto.Message {
	m := &cdproto.Message{ID: id, SessionID: target.SessionID(sess), Method: cdproto.MethodType(method)}
	if params != "" {
		m.Params = jsonv2.Value(params)
	}
	return m
}

func event(sess, method, params string) *cdproto.Message { return msg(0, sess, method, params) }

func TestFenceOutbound(t *testing.T) {
	fc := newFakeConn()
	pol := newFencePolicy()
	f := newFence(pol, fc)
	defer f.Close()
	ctx := context.Background()

	write := func(m *cdproto.Message) { _ = f.Write(ctx, m) }
	read := func() *cdproto.Message {
		m := new(cdproto.Message)
		c, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if err := f.Read(c, m); err != nil {
			t.Fatalf("read: %v", err)
		}
		return m
	}
	denied := func(m *cdproto.Message) {
		t.Helper()
		before := len(fc.methods())
		write(m)
		r := read()
		if r.ID != m.ID || r.Error == nil || !strings.Contains(r.Error.Message, "does not allow") {
			t.Errorf("%s was not refused: %+v", m.Method, r)
		}
		if len(fc.methods()) != before {
			t.Errorf("%s reached the browser", m.Method)
		}
	}

	// the tab werkz creates is learned from the answer
	denied(msg(1, "", "Target.createTarget", `{"url":"https://example.com"}`))
	denied(msg(2, "", "Target.createTarget", `{"url":"about:blank","browserContextId":"X"}`))
	write(msg(3, "", "Target.createTarget", `{"url":"about:blank","newWindow":true}`))
	fc.in <- &cdproto.Message{ID: 3, Result: jsonv2.Value(`{"targetId":"TAB"}`)}
	read()
	if !pol.has("TAB") || !pol.isTab("TAB") {
		t.Fatal("the new tab was not registered")
	}
	// browser-wide and foreign commands never reach Chrome
	for i, m := range []*cdproto.Message{
		msg(10, "", "Browser.close", ""),
		msg(11, "", "Browser.setDownloadBehavior", `{"behavior":"deny"}`),
		msg(12, "", "Target.attachToTarget", `{"targetId":"USERTAB","flatten":true}`),
		msg(13, "", "Target.closeTarget", `{"targetId":"USERTAB"}`),
		msg(14, "", "Target.setAutoAttach", `{"autoAttach":true,"waitForDebuggerOnStart":true}`),
		msg(15, "", "Target.createBrowserContext", ""),
		msg(16, "", "Storage.getCookies", ""),
		msg(17, "", "Fetch.enable", ""),  // browser-level Fetch
		msg(18, "SX", "Page.enable", ""), // a session werkz did not open
	} {
		_ = i
		denied(m)
	}
	// getTargets is answered with nothing
	write(msg(20, "", "Target.getTargets", ""))
	if r := read(); r.ID != 20 || r.Error != nil || !strings.Contains(string(r.Result), `"targetInfos":[]`) {
		t.Errorf("getTargets: %+v", r)
	}
	// our own attach
	write(msg(21, "", "Target.attachToTarget", `{"targetId":"TAB","flatten":true}`))
	fc.in <- &cdproto.Message{ID: 21, Result: jsonv2.Value(`{"sessionId":"S1"}`)}
	read()
	write(msg(22, "S1", "Page.enable", ""))
	write(msg(23, "S1", "Target.setAutoAttach", `{"autoAttach":true,"flatten":true}`))
	write(msg(24, "S1", "Target.setDiscoverTargets", `{"discover":true}`))
	if r := read(); r.ID != 24 || r.Error != nil {
		t.Errorf("session discovery should be swallowed: %+v", r)
	}
	denied(msg(25, "S1", "Target.createTarget", `{"url":"about:blank"}`))
	denied(msg(26, "S1", "Browser.setDownloadBehavior", ""))
	denied(msg(27, "S1", "Network.getAllCookies", ""))
	denied(msg(28, "S1", "Storage.clearDataForOrigin", ""))
	write(msg(29, "", "Target.closeTarget", `{"targetId":"TAB"}`))
	want := []string{"Target.createTarget", "Target.attachToTarget", "Page.enable", "Target.setAutoAttach", "Target.closeTarget"}
	if got := fc.methods(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("reached the browser: %v, want %v", got, want)
	}
}

func TestFenceInbound(t *testing.T) {
	fc := newFakeConn()
	pol := newFencePolicy()
	pol.addTab("TAB")
	f := newFence(pol, fc)
	defer f.Close()
	read := func() (*cdproto.Message, bool) {
		m := new(cdproto.Message)
		c, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		return m, f.Read(c, m) == nil
	}
	info := func(id, opener string) string {
		return fmt.Sprintf(`{"targetInfo":{"targetId":%q,"openerId":%q,"type":"page","url":"https://secret.example/inbox"}}`, id, opener)
	}
	// somebody else's tab and its popup never come through
	fc.in <- event("", "Target.targetCreated", info("USER", ""))
	fc.in <- event("", "Target.targetInfoChanged", info("USER", ""))
	fc.in <- event("", "Target.targetCreated", info("USERPOP", "USER"))
	fc.in <- event("", "Target.targetDestroyed", `{"targetId":"USER"}`)
	fc.in <- event("", "Target.attachedToTarget", `{"sessionId":"SU","targetInfo":{"targetId":"USER"}}`)
	fc.in <- event("SU", "Page.loadEventFired", `{}`)
	fc.in <- event("", "Browser.downloadWillBegin", `{}`)
	if m, ok := read(); ok {
		t.Fatalf("a foreign event got through: %s %s", m.Method, m.Params)
	}
	if pol.has("USER") || pol.has("USERPOP") {
		t.Fatal("foreign targets were registered")
	}
	// werkz's tab and the popups it opens do
	fc.in <- event("", "Target.targetInfoChanged", info("TAB", ""))
	fc.in <- event("", "Target.targetCreated", info("POP", "TAB"))
	fc.in <- event("", "Target.targetCreated", info("NESTED", "POP"))
	for _, id := range []string{"TAB", "POP", "NESTED"} {
		m, ok := read()
		if !ok || !strings.Contains(string(m.Params), `"`+id+`"`) {
			t.Fatalf("event for %s: %v %v", id, m, ok)
		}
	}
	fc.in <- event("", "Target.attachedToTarget", `{"sessionId":"ST","targetInfo":{"targetId":"TAB"}}`)
	fc.in <- event("ST", "Target.attachedToTarget", `{"sessionId":"SF","targetInfo":{"targetId":"IFRAME"}}`)
	fc.in <- event("SF", "Network.requestWillBeSent", `{}`)
	for i := 0; i < 3; i++ {
		if _, ok := read(); !ok {
			t.Fatalf("event %d of werkz's tab was dropped", i)
		}
	}
	if pol.isLost() {
		t.Fatal("tab reported lost")
	}
	// the user closing werkz's tab is noticed
	fc.in <- event("", "Target.targetDestroyed", `{"targetId":"TAB"}`)
	if _, ok := read(); !ok || !pol.isLost() {
		t.Fatal("closing werkz's tab was not noticed")
	}
}

// userChrome is a Chrome the test starts the way a user would: headless, its
// own profile, a debugging port.
type userChrome struct {
	t    *testing.T
	cmd  *exec.Cmd
	port string
	ws   string
	home string
	done chan struct{}
}

func startUserChrome(t *testing.T) *userChrome {
	t.Helper()
	exe, err := Find()
	if err != nil {
		t.Skip("no browser installed: ", err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("unix only")
	}
	// only one Chrome at a time: let go of the shared launch-mode browser
	if shared != nil {
		_ = shared.Close()
		shared, sharedErr, sharedOnce = nil, nil, sync.Once{}
	}
	prof, home := t.TempDir(), t.TempDir()
	cmd := exec.Command(exe, "--headless=new", "--remote-debugging-port=0", "--user-data-dir="+prof,
		"--no-first-run", "--no-default-browser-check", "--disable-extensions", "about:blank")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // so the whole process tree can be stopped
	cmd.Env = append(os.Environ(), "HOME="+home, "XDG_DOWNLOAD_DIR="+filepath.Join(home, "Downloads"))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	u := &userChrome{t: t, cmd: cmd, home: home, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(u.done) }()
	t.Cleanup(u.kill)
	ws, err := readActivePort(prof)
	if err != nil {
		t.Fatal(err)
	}
	u.ws = ws
	pu, _ := url.Parse(ws)
	u.port = pu.Port()
	return u
}

func (u *userChrome) kill() {
	_ = syscall.Kill(-u.cmd.Process.Pid, syscall.SIGKILL)
	<-u.done
}

func (u *userChrome) running() bool {
	select {
	case <-u.done:
		return false
	default:
		return true
	}
}

type tabInfo struct {
	ID, Type, URL string
}

func (u *userChrome) tabs() []tabInfo {
	u.t.Helper()
	resp, err := http.Get("http://127.0.0.1:" + u.port + "/json/list")
	if err != nil {
		u.t.Fatal(err)
	}
	defer resp.Body.Close()
	var all []tabInfo
	if err := json.NewDecoder(resp.Body).Decode(&all); err != nil {
		u.t.Fatal(err)
	}
	var pages []tabInfo
	for _, ti := range all {
		if ti.Type == "page" {
			pages = append(pages, ti)
		}
	}
	return pages
}

func (u *userChrome) find(sub string) *tabInfo {
	for _, ti := range u.tabs() {
		if strings.HasSuffix(ti.URL, sub) {
			return &ti
		}
	}
	return nil
}

func (u *userChrome) openTab(rawURL string) tabInfo {
	u.t.Helper()
	req, _ := http.NewRequest(http.MethodPut, "http://127.0.0.1:"+u.port+"/json/new?"+rawURL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		u.t.Fatal(err)
	}
	defer resp.Body.Close()
	var ti tabInfo
	if err := json.NewDecoder(resp.Body).Decode(&ti); err != nil {
		u.t.Fatal(err)
	}
	return ti
}

// userContext attaches the test to a tab, to act as the user in it.
func (u *userChrome) userContext(id string) context.Context {
	u.t.Helper()
	ac, cancel := remote.NewAllocator(context.Background(), u.ws, remote.NoModifyURL)
	ctx, cancel2 := chromedp.NewContext(ac, chromedp.WithTargetID(target.ID(id)), chromedp.WithDetachOnCancel())
	u.t.Cleanup(func() { cancel2(); cancel() })
	if err := chromedp.Do(ctx); err != nil { // the first run attaches, and must carry no timeout
		u.t.Fatal(err)
	}
	return ctx
}

// userEval runs script in a tab as the user's own click would.
func userEval(t *testing.T, ctx context.Context, script string) {
	t.Helper()
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := chromedp.Call(c, rt.Evaluate, rt.EvaluateParams{Expression: script, UserGesture: new(true)}); err != nil {
		t.Fatal(err)
	}
}

func eventually(t *testing.T, what string, d time.Duration, f func() bool) {
	t.Helper()
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if f() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestConnectedMode(t *testing.T) {
	u := startUserChrome(t)
	other, _ := countingServer(t, `<h1>Second server</h1>`)
	denied, dc := countingServer(t, `<h1>Denied</h1>`)
	mux := http.NewServeMux()
	mux.HandleFunc("/f.bin", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="report.bin"`)
		fmt.Fprint(w, "DATA")
	})
	mux.HandleFunc("/plain.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "plain")
	})
	mux.HandleFunc("/user", html(`<h1>Signed in as the user</h1><button onclick="window.open('/userpopup')">User popup</button>`))
	mux.HandleFunc("/userpopup", html(`<h1>Opened by the user</h1>`))
	mux.HandleFunc("/", html(`<h1>Werkz page</h1><button id=b onclick="document.getElementById('out').textContent='clicked'">Go</button><div id=out>idle</div>
<label>Name <input type=text name=n></label>
<script>function rawOpen(u){var k=Object.getOwnPropertyNames(window).filter(function(n){return /^__wz[0-9a-f]+o$/.test(n)})[0];window[k](u)}</script>
<button onclick="rawOpen('`+other.URL+`/p')">Open ok</button>
<button onclick="rawOpen('`+denied.URL+`/p')">Open denied</button>
<a href="`+denied.URL+`/q" target=_blank>Blank denied</a>
<a href="/f.bin">Get file</a><a href="/plain.txt" download="note.txt">Get plain</a>`))
	s := serve(t, mux.ServeHTTP)

	user := u.openTab(s.URL + "/user")
	userCtx := u.userContext(user.ID)
	eventually(t, "the user's tab to load", 5*time.Second, func() bool { return u.find("/user") != nil })
	tabsBefore := len(u.tabs())

	allowHook.Store(func(h string) bool { return h == hostOf(s.URL) || h == hostOf(other.URL) })
	b, err := Connect(ctx5(t), u.port, Options{
		ShotDir:   filepath.Join(t.TempDir(), "shots"),
		AllowHost: func(h string) bool { return allowHook.Load().(func(string) bool)(h) },
	})
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = b.Close()
		}
	}()
	if !b.Connected() || b.Headless() {
		t.Errorf("Connected=%v Headless=%v", b.Connected(), b.Headless())
	}
	if err := b.SetHeadless(ctx5(t), true); err == nil || !strings.Contains(err.Error(), "its window is yours to show or hide") {
		t.Errorf("SetHeadless: %v", err)
	}
	if n := len(u.tabs()); n != tabsBefore+1 {
		t.Fatalf("tabs after Connect = %d, want %d: werkz must open exactly one", n, tabsBefore+1)
	}
	if !b.Alive() {
		t.Fatal("not alive after Connect")
	}

	// the basics work on werkz's tab
	snap, err := b.Navigate(ctx5(t), s.URL+"/")
	if err != nil || !strings.Contains(snap.Tree, "Werkz page") || snap.Status != 200 {
		t.Fatalf("navigate: %v %+v", err, snap)
	}
	snap, err = b.Click(ctx5(t), refOf(t, snap.Tree, "button", "Go"), Allow{})
	if err != nil || !strings.Contains(snap.Tree, "clicked") {
		t.Fatalf("click: %v\n%v", err, snap)
	}
	snap, err = b.Type(ctx5(t), refOf(t, snap.Tree, "textbox", "Name"), "Ada", true, false, Allow{})
	if err != nil || !strings.Contains(snap.Tree, `value="Ada"`) {
		t.Fatalf("type: %v\n%v", err, snap)
	}
	if _, err := b.Screenshot(ctx5(t), false, false); err != nil {
		t.Errorf("screenshot: %v", err)
	}
	if _, err := b.ReadPage(ctx5(t), true); err != nil {
		t.Errorf("read page: %v", err)
	}
	// werkz cannot list the user's tabs
	if ts, err := chromedp.Targets(b.tab); err != nil || len(ts) != 0 {
		t.Errorf("Targets = %v, %v: the user's tabs must stay invisible", ts, err)
	}
	// a popup the user's tab opens is none of werkz's business
	_, err = b.Navigate(ctx5(t), s.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	userEval(t, userCtx, `window.open('/userpopup')`)
	eventually(t, "the user's popup", 5*time.Second, func() bool { return u.find("/userpopup") != nil })
	time.Sleep(popupWait + time.Second)
	if u.find("/userpopup") == nil {
		t.Error("the user's popup was closed")
	}
	if u.find("/user") == nil {
		t.Error("the user's tab is gone")
	}
	snap, err = b.snapshotFor(t)
	if err != nil || len(snap.Blocked) != 0 || strings.Contains(snap.URL, "userpopup") {
		t.Errorf("the user's popup leaked into werkz: %v %+v", err, snap)
	}

	// werkz's own popups are handled
	snap, _ = b.Navigate(ctx5(t), s.URL+"/")
	base := len(u.tabs())
	got, err := b.Click(ctx5(t), refOf(t, snap.Tree, "button", "Open ok"), Allow{})
	if err != nil || !strings.HasPrefix(got.URL, other.URL+"/p") || !strings.Contains(got.Tree, "Second server") {
		t.Fatalf("popup to an allowed host: %v %+v", err, got)
	}
	snap, _ = b.Navigate(ctx5(t), s.URL+"/")
	got, err = b.Click(ctx5(t), refOf(t, snap.Tree, "button", "Open denied"), Allow{})
	if err != nil || !hasNote(got.Blocked, "popup to "+denied.URL+"/p") || !strings.HasPrefix(got.URL, s.URL) {
		t.Fatalf("popup to a denied host: %v %+v", err, got)
	}
	got, err = b.Click(ctx5(t), refOf(t, snap.Tree, "link", "Blank denied"), Allow{})
	if err != nil || !hasNote(got.Blocked, denied.URL+"/q") || !strings.HasPrefix(got.URL, s.URL) {
		t.Fatalf("target=_blank to a denied host: %v %+v", err, got)
	}
	t.Log("denied server hits:", dc.n("GET /p"), dc.n("GET /q"))
	time.Sleep(popupWait + 500*time.Millisecond)
	if n := len(u.tabs()); n != base {
		t.Errorf("tabs = %d, want %d: werkz's popups were left open", n, base)
	}

	// downloads
	snap, _ = b.Navigate(ctx5(t), s.URL+"/")
	got, err = b.Click(ctx5(t), refOf(t, snap.Tree, "link", "Get file"), Allow{})
	if err != nil || len(got.Downloads) != 1 || got.Downloads[0] != "report.bin (blocked: downloads are off)" || !strings.Contains(got.Tree, "Werkz page") {
		t.Fatalf("download without Allow: %v %+v", err, got)
	}
	got, err = b.Click(ctx5(t), refOf(t, snap.Tree, "link", "Get plain"), Allow{})
	if err != nil || len(got.Downloads) != 1 || got.Downloads[0] != "note.txt (blocked: downloads are off)" {
		t.Fatalf("download attribute without Allow: %v %+v", err, got)
	}
	noFiles := func() bool {
		var found []string
		_ = filepath.Walk(u.home, func(p string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() && (fi.Name() == "report.bin" || fi.Name() == "note.txt") {
				found = append(found, p)
			}
			return nil
		})
		return len(found) == 0
	}
	time.Sleep(500 * time.Millisecond)
	if !noFiles() {
		t.Error("a blocked download reached the disk")
	}
	got, err = b.Click(ctx5(t), refOf(t, snap.Tree, "link", "Get file"), Allow{DownloadDir: t.TempDir()})
	if err != nil || len(got.Downloads) != 1 || got.Downloads[0] != "report.bin (saved by Chrome to its download folder)" {
		t.Fatalf("download with Allow: %v %+v", err, got)
	}
	eventually(t, "Chrome to save the allowed download", 5*time.Second, func() bool { return !noFiles() })

	// Close leaves the user's Chrome and tabs alone
	userURL := u.find("/user").URL
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	if b.Alive() {
		t.Error("Alive after Close")
	}
	if !u.running() {
		t.Fatal("Close stopped the user's Chrome")
	}
	eventually(t, "werkz's tab to close", 5*time.Second, func() bool { return len(u.tabs()) == tabsBefore+1 }) // the user's popup is the one extra
	ut := u.find("/user")
	if ut == nil || ut.URL != userURL || ut.ID != user.ID {
		t.Errorf("the user's tab changed: %+v (was %s)", ut, userURL)
	}
	if u.find("/userpopup") == nil {
		t.Error("the user's popup was closed")
	}
	if n := len(u.tabs()); n != tabsBefore+1 { // the user's popup is the one extra
		t.Errorf("tabs after Close = %d, want %d", n, tabsBefore+1)
	}
	if _, err := b.Navigate(ctx5(t), s.URL); err == nil {
		t.Error("Navigate worked after Close")
	}

	// the user closing werkz's tab makes Alive false
	b2, err := Connect(ctx5(t), "http://127.0.0.1:"+u.port, Options{ShotDir: filepath.Join(t.TempDir(), "shots")})
	if err != nil {
		t.Fatal(err)
	}
	defer b2.Close()
	if _, err := b2.Navigate(ctx5(t), s.URL+"/"); err != nil {
		t.Fatal(err)
	}
	var mine string
	for _, ti := range u.tabs() {
		if strings.HasPrefix(ti.URL, s.URL+"/") && ti.URL == s.URL+"/" {
			mine = ti.ID
		}
	}
	if mine == "" {
		t.Fatal("werkz's second tab not found")
	}
	resp, err := http.Get("http://127.0.0.1:" + u.port + "/json/close/" + mine)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	eventually(t, "Alive to turn false after the user closed werkz's tab", 5*time.Second, func() bool { return !b2.Alive() })
	if u.find("/user") == nil {
		t.Error("the user's tab is gone")
	}

	// and so does the user quitting Chrome
	b3, err := Connect(ctx5(t), u.port, Options{ShotDir: filepath.Join(t.TempDir(), "shots")})
	if err != nil {
		t.Fatal(err)
	}
	defer b3.Close()
	if !b3.Alive() {
		t.Fatal("reconnect: not alive")
	}
	u.kill()
	eventually(t, "Alive to turn false after Chrome quit", 5*time.Second, func() bool { return !b3.Alive() })
}
