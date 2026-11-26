package browser

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestConnectedActions drives the new actions in the user's own Chrome: tabs
// stay werkz's own, and every route to a new window is a navigation of the tab
// that the policy judges before any request.
func TestConnectedActions(t *testing.T) {
	u := startUserChrome(t)
	denied, dc := countingServer(t, `<h1>Denied</h1>`)
	ok, _ := countingServer(t, `<h1>Allowed</h1>`)
	mux := http.NewServeMux()
	mux.HandleFunc("/user", html(`<h1>The user's own page</h1>`))
	mux.HandleFunc("/", html(`<h1>Werkz page</h1><input id=t aria-label=Name>
<a href="`+denied.URL+`/a" target=_blank>Blank denied</a>
<button onclick="window.open('`+denied.URL+`/b')">Open denied</button>
<button onclick="window.open('`+ok.URL+`/ok')">Open allowed</button>
<div id=box style="height:80px;overflow:auto"><button>First</button><div style="height:600px"></div></div>`))
	s := serve(t, mux.ServeHTTP)
	user := u.openTab(s.URL + "/user")
	eventually(t, "the user's tab", 5*time.Second, func() bool { return u.find("/user") != nil })
	before := len(u.tabs())
	allowHook.Store(func(h string) bool { return h == hostOf(s.URL) || h == hostOf(ok.URL) })
	b, err := Connect(ctx5(t), u.port, Options{
		ShotDir:   filepath.Join(t.TempDir(), "shots"),
		AllowHost: func(h string) bool { return allowHook.Load().(func(string) bool)(h) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	snap, err := b.Navigate(ctx5(t), s.URL+"/")
	if err != nil {
		t.Fatal(err)
	}

	// the actions work through the fence
	if _, err := b.Click(ctx5(t), refNamed(t, snap.Tree, "Name"), Allow{}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.TypeText(ctx5(t), "Ada", Allow{}); err != nil {
		t.Fatal(err)
	}
	if got := ev(t, b, `document.getElementById('t').value`); got != "Ada" {
		t.Errorf("TypeText: %q", got)
	}
	if _, err := b.Key(ctx5(t), "ctrl+a Backspace", 1, Allow{}); err != nil || ev(t, b, `document.getElementById('t').value`) != "" {
		t.Errorf("Key: %v", err)
	}
	if _, err := b.SetValue(ctx5(t), refNamed(t, snap.Tree, "Name"), "Bob"); err != nil || ev(t, b, `document.getElementById('t').value`) != "Bob" {
		t.Errorf("SetValue: %v", err)
	}
	if _, err := b.Scroll(ctx5(t), refNamed(t, snap.Tree, "First"), 0, 0, "down", 2); err != nil || evNum(t, b, `document.getElementById('box').scrollTop`) < 100 {
		t.Errorf("Scroll: %v", err)
	}
	if lines, err := b.Find(ctx5(t), "denied", 0); err != nil || len(lines) == 0 {
		t.Errorf("Find: %v %v", lines, err)
	}
	if _, text, err := b.PageText(ctx5(t)); err != nil || !strings.Contains(text, "Werkz page") {
		t.Errorf("PageText: %q %v", text, err)
	}
	if z, err := b.Zoom(ctx5(t), 0, 0, 120, 60); err != nil || z.Width != 120 {
		t.Errorf("Zoom: %+v %v", z, err)
	}
	if err := b.Resize(ctx5(t), "", 500, 400); err != nil || evNum(t, b, `window.innerWidth`) != 500 {
		t.Errorf("Resize: %v", err)
	}
	if err := b.Resize(ctx5(t), "desktop", 0, 0); err != nil {
		t.Errorf("Resize desktop: %v", err)
	}

	// no request reaches a host the policy refuses, and no tab opens
	tabsNow := len(u.tabs())
	for _, name := range []string{"Blank denied", "Open denied"} {
		got, err := b.Click(ctx5(t), refNamed(t, snap.Tree, name), Allow{})
		if err != nil || !hasNote(got.Blocked, denied.URL) || !strings.HasPrefix(got.URL, s.URL) {
			t.Fatalf("%s: %v %+v", name, err, got)
		}
	}
	got, err := b.Click(ctx5(t), refNamed(t, snap.Tree, "Open allowed"), Allow{})
	if err != nil || !strings.HasPrefix(got.URL, ok.URL+"/ok") {
		t.Fatalf("window.open to an allowed host: %v %+v", err, got)
	}
	time.Sleep(popupWait + 500*time.Millisecond)
	if n := dc.n("GET /a") + dc.n("GET /b"); n != 0 {
		t.Errorf("the denied host received %d requests", n)
	}
	if n := len(u.tabs()); n != tabsNow {
		t.Errorf("tabs = %d, want %d: a popup opened in the user's Chrome", n, tabsNow)
	}

	// tabs: only werkz's own
	tabs, err := b.Tabs(ctx5(t))
	if err != nil || len(tabs) != 1 || strings.Contains(tabs[0].URL, "/user") {
		t.Fatalf("Tabs: %+v, %v", tabs, err)
	}
	first := tabs[0].ID
	if first == user.ID {
		t.Fatal("the user's tab is listed as werkz's")
	}
	if snap, err = b.NewTab(ctx5(t), s.URL+"/"); err != nil || !strings.Contains(snap.Tree, "Werkz page") {
		t.Fatalf("NewTab: %v", err)
	}
	if n := len(u.tabs()); n != tabsNow+1 {
		t.Errorf("tabs after NewTab = %d, want %d", n, tabsNow+1)
	}
	tabs, _ = b.Tabs(ctx5(t))
	if len(tabs) != 2 {
		t.Fatalf("Tabs: %+v", tabs)
	}
	for _, ti := range tabs {
		if ti.ID == user.ID || strings.HasSuffix(ti.URL, "/user") {
			t.Errorf("the user's tab is listed: %+v", ti)
		}
	}
	second := tabs[1].ID
	if _, err := b.SwitchTab(ctx5(t), user.ID); err == nil {
		t.Error("switching to the user's tab worked")
	}
	if err := b.CloseTab(ctx5(t), user.ID); err == nil {
		t.Error("closing the user's tab worked")
	}
	if u.find("/user") == nil {
		t.Fatal("the user's tab is gone")
	}
	// events follow the active tab: the new tab's popups are vetted too
	page, _ := b.ReadPage(ctx5(t), false)
	got, err = b.Click(ctx5(t), refNamed(t, page, "Open denied"), Allow{})
	if err != nil || !hasNote(got.Blocked, denied.URL) {
		t.Fatalf("popup from the second tab: %v %+v", err, got)
	}
	if err := b.CloseTab(ctx5(t), first); err != nil { // the first tab carries the connection
		t.Fatal(err)
	}
	if snap, err = b.Navigate(ctx5(t), s.URL+"/"); err != nil || !strings.Contains(snap.Tree, "Werkz page") {
		t.Fatalf("navigate after closing the first tab: %v", err)
	}
	if err := b.CloseTab(ctx5(t), second); err != nil {
		t.Fatal(err)
	}
	tabs, _ = b.Tabs(ctx5(t))
	if len(tabs) != 1 || tabs[0].URL != "about:blank" || !b.Alive() {
		t.Fatalf("after closing the last tab: %+v alive=%v", tabs, b.Alive())
	}
	eventually(t, "werkz's tabs to settle", 5*time.Second, func() bool { return len(u.tabs()) == before+1 })
	if u.find("/user") == nil {
		t.Error("the user's tab is gone")
	}
}
