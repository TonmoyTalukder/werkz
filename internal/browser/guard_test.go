package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/chromedp"
)

// counter is a server that records how often each "METHOD path" was hit.
type counter struct {
	mu   sync.Mutex
	hits map[string]int
}

func (c *counter) n(key string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits[key]
}

func countingServer(t *testing.T, page string) (*httptest.Server, *counter) {
	t.Helper()
	c := &counter{hits: map[string]int{}}
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.hits[r.Method+" "+r.URL.Path]++
		c.mu.Unlock()
		if r.Method == "POST" {
			html("<h1>posted</h1>")(w, r)
			return
		}
		html(page)(w, r)
	})
	return s, c
}

func hasNote(notes []string, sub string) bool {
	for _, n := range notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

func TestSubmitBlockedUnlessAllowed(t *testing.T) {
	b := br(t)
	s, c := countingServer(t, `<style>body{margin:0}
#plain,#ov{position:absolute;left:10px;top:60px;width:200px;height:40px}#ov{background:rgba(0,0,0,.1)}
#other{position:absolute;left:10px;top:200px}</style>
<form id=f method=post action="/post1"><input type=text aria-label=Name onchange="this.form.submit()"></form>
<button id=plain type=button onclick="document.getElementById('f').submit()">Plain</button><div id=ov></div>
<button type=button onclick="document.getElementById('f').submit()" style="position:absolute;left:300px;top:60px">Js submit</button>
<button type=button id=other>Other</button>
<form method=post action="/post3"><input type=submit id=s style="display:none"></form>
<label for=s tabindex=0 role=button style="position:absolute;left:300px;top:300px">Go label</label>`)
	snap, err := b.Navigate(ctx5(t), s.URL)
	if err != nil {
		t.Fatal(err)
	}
	post1, post3 := "form submit to "+s.URL+"/post1 (needs the user's approval)", "form submit to "+s.URL+"/post3 (needs the user's approval)"

	// an overlay sits over the button that submits: nothing is clicked
	if _, err := b.Click(ctx5(t), refOf(t, snap.Tree, "button", "Plain"), Allow{Submit: true}); err == nil ||
		!strings.Contains(err.Error(), "covered by another element") || !strings.Contains(err.Error(), "ov") {
		t.Errorf("overlay: %v", err)
	}
	if c.n("POST /post1") != 0 {
		t.Fatal("overlay click submitted")
	}
	// a plain-looking button whose script submits
	js := refOf(t, snap.Tree, "button", "Js submit")
	got, err := b.Click(ctx5(t), js, Allow{})
	if err != nil || !hasNote(got.Blocked, post1) || c.n("POST /post1") != 0 || !strings.HasPrefix(got.URL, s.URL) {
		t.Fatalf("script submit: %v %+v", err, got)
	}
	// onchange -> form.submit() fires when the field loses focus later
	if _, err := b.Type(ctx5(t), refOf(t, snap.Tree, "textbox", "Name"), "x", true, false, Allow{}); err != nil {
		t.Fatal(err)
	}
	got, err = b.Click(ctx5(t), refOf(t, snap.Tree, "button", "Other"), Allow{})
	if err != nil || !hasNote(got.Blocked, post1) || c.n("POST /post1") != 0 {
		t.Fatalf("onchange submit: %v %+v", err, got)
	}
	// a label for a submit button
	lab := refOf(t, snap.Tree, "button", "Go label")
	if el, err := b.Inspect(ctx5(t), lab); err != nil || !el.Submits || el.Tag != "label" {
		t.Errorf("label inspect: %+v %v", el, err)
	}
	got, err = b.Click(ctx5(t), lab, Allow{})
	if err != nil || !hasNote(got.Blocked, post3) || c.n("POST /post3") != 0 {
		t.Fatalf("label submit: %v %+v", err, got)
	}
	// the network layer blocks a POST document, whatever the page script did
	got, err = b.Click(ctx5(t), js, Allow{})
	if err != nil || !hasNote(got.Blocked, post1) || c.n("POST /post1") != 0 {
		t.Fatalf("network POST guard: %v %+v", err, got)
	}

	// allowed
	got, err = b.Click(ctx5(t), js, Allow{Submit: true})
	if err != nil || c.n("POST /post1") != 1 || !strings.HasSuffix(got.URL, "/post1") || !strings.Contains(got.Tree, "posted") || len(got.Blocked) != 0 {
		t.Fatalf("allowed submit: %v %+v (posts %d)", err, got, c.n("POST /post1"))
	}
	// and the allowance ends with the action
	snap, err = b.Navigate(ctx5(t), s.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, err = b.Click(ctx5(t), refOf(t, snap.Tree, "button", "Js submit"), Allow{})
	if err != nil || !hasNote(got.Blocked, post1) || c.n("POST /post1") != 1 {
		t.Fatalf("allowance leaked: %v %+v", err, got)
	}
	got, err = b.Click(ctx5(t), refOf(t, snap.Tree, "button", "Go label"), Allow{Submit: true})
	if err != nil || c.n("POST /post3") != 1 || !strings.HasSuffix(got.URL, "/post3") {
		t.Fatalf("label allowed: %v %+v", err, got)
	}
	// Enter in a form field is a submission too
	s2, c2 := countingServer(t, `<form method=post action="/enter"><input type=text aria-label=Q></form>`)
	snap, err = b.Navigate(ctx5(t), s2.URL)
	if err != nil {
		t.Fatal(err)
	}
	q := refOf(t, snap.Tree, "textbox", "Q")
	got, err = b.Type(ctx5(t), q, "hi", true, true, Allow{})
	if err != nil || c2.n("POST /enter") != 0 || !hasNote(got.Blocked, "form submit to "+s2.URL+"/enter") {
		t.Fatalf("enter blocked: %v %+v", err, got)
	}
	got, err = b.Type(ctx5(t), q, "hi", true, true, Allow{Submit: true})
	if err != nil || c2.n("POST /enter") != 1 {
		t.Fatalf("enter allowed: %v %+v", err, got)
	}
}

func TestClickRefusesCoveredElement(t *testing.T) {
	b := br(t)
	s := serve(t, html(`<button id=b style="position:absolute;left:20px;top:20px;width:150px;height:40px"
 onclick="document.title='clicked'">Target</button>
<div id=cover style="position:absolute;left:0;top:0;width:400px;height:200px"></div>
<label style="position:absolute;left:20px;top:300px"><input type=checkbox aria-label=Tick></label>`))
	snap, err := b.Navigate(ctx5(t), s.URL)
	if err != nil {
		t.Fatal(err)
	}
	ref := refOf(t, snap.Tree, "button", "Target")
	_, err = b.Click(ctx5(t), ref, Allow{})
	if err == nil || !strings.Contains(err.Error(), ref+" is covered by another element (<div#cover>): ReadPage again or close the overlay") {
		t.Fatalf("covered: %v", err)
	}
	if title, _ := chromedp.Run(b.tab, chromedp.Title()); title == "clicked" {
		t.Error("covered button was clicked")
	}
	// a click that lands on a child of the element is fine
	if _, err := b.Click(ctx5(t), refOf(t, snap.Tree, "checkbox", "Tick"), Allow{}); err != nil {
		t.Errorf("uncovered click: %v", err)
	}
}

func TestTypeChecksFocus(t *testing.T) {
	b := br(t)
	s := serve(t, html(`<input type=text aria-label=A onfocus="document.getElementById('b').focus()">
<input type=text id=b aria-label=B>
<input type=text aria-label=C oninput="document.getElementById('pw').focus()">
<input type=password id=pw aria-label=Pw>
<input type=text aria-label=D onfocus="document.getElementById('pw').focus()">`))
	snap, err := b.Navigate(ctx5(t), s.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Type(ctx5(t), refOf(t, snap.Tree, "textbox", "A"), "x", true, false, Allow{}); err == nil || !strings.Contains(err.Error(), "focus moved to another field") {
		t.Errorf("moved focus: %v", err)
	}
	if _, err := b.Type(ctx5(t), refOf(t, snap.Tree, "textbox", "D"), "x", true, false, Allow{}); err == nil || !strings.Contains(err.Error(), "never types into password") {
		t.Errorf("focus into secret: %v", err)
	}
	_, err = b.Type(ctx5(t), refOf(t, snap.Tree, "textbox", "C"), "x", true, true, Allow{Submit: true})
	if err == nil || !strings.Contains(err.Error(), "not pressing Enter") {
		t.Errorf("enter into secret: %v", err)
	}
}

func TestHistoryApprovalIsNarrow(t *testing.T) {
	b := br(t)
	other := serve(t, html(`<h1>Elsewhere</h1>`))
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/b" {
			html(`<h1>B</h1><script>function go(){location.href="`+other.URL+`/x"}
setTimeout(go,300);addEventListener('pageshow',function(){setTimeout(go,300)})</script>`)(w, r)
			return
		}
		html(`<h1>A</h1>`)(w, r)
	})
	allowHook.Store(func(h string) bool { return h == hostOf(s.URL) })
	snap, err := b.Navigate(ctx5(t), s.URL+"/b")
	if err != nil || !hasNote(snap.Blocked, other.URL) {
		t.Fatalf("first load: %v %+v", err, snap)
	}
	if _, err = b.Navigate(ctx5(t), s.URL+"/a"); err != nil {
		t.Fatal(err)
	}
	snap, err = b.Navigate(ctx5(t), "back")
	if err != nil {
		t.Fatal(err)
	}
	// let the page's timer fire after the history move
	time.Sleep(600 * time.Millisecond)
	snap2, err := b.snapshotFor(t)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(snap2.URL, "/b") || strings.Contains(snap2.Tree, "Elsewhere") {
		t.Errorf("history approval let the page leave: %s\n%s", snap2.URL, snap2.Tree)
	}
	if !hasNote(snap.Blocked, other.URL) && !hasNote(snap2.Blocked, other.URL) {
		t.Errorf("redirect after back not blocked: %v %v", snap.Blocked, snap2.Blocked)
	}
	// plain back/forward/reload still work
	if snap, err = b.Navigate(ctx5(t), "forward"); err != nil || !strings.HasSuffix(snap.URL, "/a") {
		t.Errorf("forward: %v %v", err, snap)
	}
	if snap, err = b.Navigate(ctx5(t), "reload"); err != nil || !strings.HasSuffix(snap.URL, "/a") || !strings.Contains(snap.Tree, "A") {
		t.Errorf("reload: %v %v", err, snap)
	}
}

func targetCount(t *testing.T, b *Browser) int {
	t.Helper()
	ts, err := chromedp.Targets(b.tab)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ti := range ts {
		if ti.Type == "page" {
			n++
		}
	}
	return n
}

func TestPopups(t *testing.T) {
	b := br(t)
	other, _ := countingServer(t, `<h1>Second server</h1>`)
	denied, dc := countingServer(t, `<h1>Denied</h1>`)
	s := serve(t, html(`<script>function rawOpen(u){var k=Object.getOwnPropertyNames(window).filter(function(n){return /^__wz[0-9a-f]+o$/.test(n)})[0];window[k](u)}</script><button onclick="rawOpen('`+other.URL+`/p')">Open ok</button>
<a href="`+other.URL+`/q" target=_blank>Blank ok</a>
<button onclick="rawOpen('`+denied.URL+`/p')">Open denied</button>
<a href="`+denied.URL+`/q" target=_blank>Blank denied</a>
<button onclick="rawOpen('about:blank')">Open empty</button>`))
	allowHook.Store(func(h string) bool { return h == hostOf(s.URL) || h == hostOf(other.URL) })
	base := targetCount(t, b)
	fresh := func() *Snapshot {
		snap, err := b.Navigate(ctx5(t), s.URL)
		if err != nil {
			t.Fatal(err)
		}
		return snap
	}

	snap := fresh()
	got, err := b.Click(ctx5(t), refOf(t, snap.Tree, "button", "Open ok"), Allow{})
	if err != nil || !strings.HasPrefix(got.URL, other.URL+"/p") || !strings.Contains(got.Tree, "Second server") {
		t.Fatalf("window.open to allowed host: %v %+v", err, got)
	}
	snap = fresh()
	got, err = b.Click(ctx5(t), refOf(t, snap.Tree, "link", "Blank ok"), Allow{})
	if err != nil || !strings.HasPrefix(got.URL, other.URL+"/q") {
		t.Fatalf("target=_blank to allowed host (rewritten to a navigation): %v %+v", err, got)
	}
	snap = fresh()
	got, err = b.Click(ctx5(t), refOf(t, snap.Tree, "button", "Open denied"), Allow{})
	if err != nil || !hasNote(got.Blocked, "popup to "+denied.URL+"/p") || !strings.HasPrefix(got.URL, s.URL) {
		t.Fatalf("window.open to denied host: %v %+v", err, got)
	}
	got, err = b.Click(ctx5(t), refOf(t, snap.Tree, "link", "Blank denied"), Allow{})
	if err != nil || !hasNote(got.Blocked, denied.URL+"/q") || !strings.HasPrefix(got.URL, s.URL) {
		t.Fatalf("target=_blank to denied host: %v %+v", err, got)
	}
	if dc.n("GET /p")+dc.n("GET /q") != 0 {
		t.Error("the denied server received a request")
	}
	// a blank popup is closed too, without hanging the action
	start := time.Now()
	if _, err = b.Click(ctx5(t), refOf(t, snap.Tree, "button", "Open empty"), Allow{}); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("blank popup held the action for %v", time.Since(start))
	}
	time.Sleep(popupWait + 500*time.Millisecond)
	if n := targetCount(t, b); n != base {
		t.Errorf("page targets = %d, want %d: popups were left open", n, base)
	}
}

func TestIframesVetted(t *testing.T) {
	b := br(t)
	sameSite, sc := countingServer(t, `<h1>same-site frame</h1>`)
	crossSite, cc := countingServer(t, `<h1>cross-site frame</h1>`)
	okFrame, oc := countingServer(t, `<h1>allowed frame</h1>`)
	cross := "http://localhost:" + mustPort(t, crossSite.URL) // another site: Chrome puts it in its own process
	deep, dpc := countingServer(t, `<h1>deep frame</h1>`)
	// an allowed cross-site frame (its own process) that embeds a disallowed one
	nested := serve(t, html(`<h1>nested</h1><iframe src="`+deep.URL+`/deep"></iframe>`))
	nestedURL := "http://localhost:" + mustPort(t, nested.URL)
	s := serve(t, html(`<iframe src="`+sameSite.URL+`/f"></iframe><iframe src="`+cross+`/f"></iframe><iframe src="`+okFrame.URL+`/f"></iframe>
<iframe src="`+nestedURL+`/n"></iframe><h1>Main</h1>`))
	allowHook.Store(func(h string) bool { return h == hostOf(s.URL) || h == hostOf(okFrame.URL) || h == hostOf(nestedURL) })
	snap, err := b.Navigate(ctx5(t), s.URL)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(800 * time.Millisecond)
	more, _ := b.snapshotFor(t)
	blocked := append(snap.Blocked, more.Blocked...)
	if !hasNote(blocked, "frame from "+sameSite.URL+"/f") || !hasNote(blocked, "frame from "+cross+"/f") {
		t.Errorf("blocked = %v", blocked)
	}
	if sc.n("GET /f") != 0 || cc.n("GET /f") != 0 {
		t.Errorf("a disallowed frame reached its server: %d %d", sc.n("GET /f"), cc.n("GET /f"))
	}
	if !hasNote(blocked, "frame from "+deep.URL+"/deep") || dpc.n("GET /deep") != 0 {
		t.Errorf("nested frame: blocked = %v, hits %d", blocked, dpc.n("GET /deep"))
	}
	if oc.n("GET /f") != 1 {
		t.Errorf("the allowed frame did not load: %d", oc.n("GET /f"))
	}
}

func mustPort(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Port()
}

func TestHostComparisonConsistent(t *testing.T) {
	var seen []string
	b := &Browser{opts: Options{AllowHost: func(h string) bool { seen = append(seen, h); return false }}}
	b.approved = "localhost:3000"
	for raw, want := range map[string]bool{
		"http://localhost:3000/x":   true,
		"http://LOCALHOST:3000/x":   true,
		"http://localhost.:3000/x":  true,
		"http://localhost:3001/x":   false,
		"http://localhost/x":        false,
		"https://other:3000/x":      false,
		"http://localhost:3000?q=1": true,
	} {
		if got := b.navAllowed(raw); got != want {
			t.Errorf("approved localhost:3000, %s: got %v want %v", raw, got, want)
		}
	}
	b.approved = "example.com"
	if !b.navAllowed("http://example.com:80/") || !b.navAllowed("https://example.com:443/y") || b.navAllowed("http://example.com:8080/") {
		t.Error("default ports are not normalised")
	}
	seen = nil
	b.approved = ""
	b.navAllowed("http://Example.com:80/")
	b.navAllowed("http://[::1]:3000/")
	b.navAllowed("https://x.test:8443/")
	if strings.Join(seen, ",") != "example.com,[::1]:3000,x.test:8443" {
		t.Errorf("AllowHost saw %v", seen)
	}
	if b.navAllowed("file:///etc/passwd") || b.navAllowed("javascript:alert(1)") {
		t.Error("non-http scheme allowed")
	}
}

func TestProfileFallbackAndClose(t *testing.T) {
	first := br(t)
	if first.tempProf {
		t.Fatal("first browser should own the profile")
	}
	dir := first.opts.ProfileDir
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	second, err := Start(ctx, Options{Headless: true, ProfileDir: dir, ShotDir: first.opts.ShotDir})
	if err != nil {
		t.Fatalf("second browser on a locked profile: %v", err)
	}
	if !second.tempProf || second.profile == dir || !strings.HasPrefix(second.profile, dir+"-") {
		t.Errorf("profile = %q (temp %v), want a %s-<pid> fallback", second.profile, second.tempProf, dir)
	}
	s := serve(t, html(`<h1>Two</h1>`))
	if snap, err := second.Navigate(ctx5(t), s.URL); err != nil || !strings.Contains(snap.Tree, "Two") {
		t.Errorf("second browser navigation: %v", err)
	}
	if !first.Alive() || !second.Alive() {
		t.Error("both browsers should be alive")
	}
	prof := second.profile
	_ = second.Close()
	if _, err := os.Stat(prof); !os.IsNotExist(err) {
		t.Errorf("fallback profile %s not removed (%v)", prof, err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("the primary profile must stay: %v", err)
	}
	if !profileLocked(dir) {
		t.Error("expected the first browser to still hold the lock")
	}
	if profileLocked(filepath.Join(t.TempDir(), "none")) {
		t.Error("a missing profile cannot be locked")
	}
}

func TestAliveAndFailedRelaunch(t *testing.T) {
	if _, err := Find(); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	b, err := Start(ctx, Options{Headless: true, ProfileDir: filepath.Join(dir, "p"), ShotDir: filepath.Join(dir, "s")})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if !b.Alive() {
		t.Fatal("fresh browser not alive")
	}
	if err := chromedp.FromContext(b.tab).Browser.Process().Kill(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !b.Alive() })
	if _, err := b.Navigate(ctx5(t), "http://127.0.0.1:1/"); err == nil {
		t.Error("a dead browser navigated")
	}
	// a failing relaunch leaves it cleanly closed
	good := b.opts.ExecPath
	b.opts.ExecPath = "/nonexistent/browser"
	if err := b.SetHeadless(ctx, true); err == nil || !strings.Contains(err.Error(), "now closed") {
		t.Errorf("failed relaunch error: %v", err)
	}
	if b.Alive() {
		t.Error("Alive after a failed relaunch")
	}
	if _, err := b.Navigate(ctx5(t), "http://127.0.0.1:1/"); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("closed browser: %v", err)
	}
	// and SetHeadless with the same mode restarts a dead browser
	b.opts.ExecPath = good
	if err := b.SetHeadless(ctx, true); err != nil || !b.Alive() {
		t.Fatalf("restart: %v alive=%v", err, b.Alive())
	}
	s := serve(t, html(`<h1>Back</h1>`))
	if snap, err := b.Navigate(ctx5(t), s.URL); err != nil || !strings.Contains(snap.Tree, "Back") {
		t.Errorf("after restart: %v", err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestBuffersAreBounded(t *testing.T) {
	r := newRing[int](3)
	for i := 1; i <= 5; i++ {
		r.add(i)
	}
	if got := r.items(); len(got) != 3 || got[0] != 3 || got[2] != 5 {
		t.Errorf("ring = %v", got)
	}
	r.clear()
	r.add(9)
	if got := r.items(); len(got) != 1 || got[0] != 9 {
		t.Errorf("after clear = %v", got)
	}

	b := &Browser{console: newRing[ConsoleEntry](ringSize), dlSeen: map[string]bool{}, dlPending: map[string]dlInfo{}}
	for i := 0; i < 1200; i++ {
		b.addConsole("log", "m", "", 0, time.Now())
	}
	if all := b.Console(false, false); len(all) != ringSize {
		t.Errorf("console kept %d", len(all))
	}
	for i := 0; i < 120; i++ {
		b.addBlocked("n" + string(rune('a'+i%26)) + string(rune('A'+i/26)))
	}
	if len(b.blocked) != maxBlocked || b.blocked[maxBlocked-1] != "n"+string(rune('a'+119%26))+string(rune('A'+119/26)) {
		t.Errorf("blocked kept %d", len(b.blocked))
	}
	for i := 0; i < 500; i++ {
		b.onDownloadBegin(downloadEvent(string(rune(i + 1000))))
	}
	if len(b.dlSeen) != maxDLSeen || len(b.dlOrder) != maxDLSeen {
		t.Errorf("dlSeen = %d / %d", len(b.dlSeen), len(b.dlOrder))
	}
}

func downloadEvent(guid string) *browser.EventDownloadWillBegin {
	return &browser.EventDownloadWillBegin{GUID: guid, SuggestedFilename: "f.bin"}
}
