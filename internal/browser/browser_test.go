package browser

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var (
	shared     *Browser
	sharedErr  error
	sharedOnce sync.Once
	sharedDir  string
	allowHook  atomic.Value // func(string) bool
)

func TestMain(m *testing.M) {
	code := m.Run()
	if shared != nil {
		_ = shared.Close()
	}
	if sharedDir != "" {
		os.RemoveAll(sharedDir)
	}
	os.Exit(code)
}

// br returns one headless browser shared by all tests: only one Chrome runs at a time.
func br(t *testing.T) *Browser {
	t.Helper()
	if _, err := Find(); err != nil {
		t.Skip("no browser installed: ", err)
	}
	sharedOnce.Do(func() {
		sharedDir, _ = os.MkdirTemp("", "werkz-browser-test")
		allowHook.Store(func(string) bool { return true })
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		shared, sharedErr = Start(ctx, Options{
			Headless:   true,
			ProfileDir: filepath.Join(sharedDir, "profile"),
			ShotDir:    filepath.Join(sharedDir, "shots"),
			AllowHost:  func(h string) bool { return allowHook.Load().(func(string) bool)(h) },
		})
	})
	if sharedErr != nil {
		t.Fatal(sharedErr)
	}
	shared.Console(false, true)
	shared.Network(false, true, "")
	allowHook.Store(func(string) bool { return true })
	return shared
}

func ctx5(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}

func serve(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

func html(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<!doctype html><html><head><title>T</title></head><body>"+body+"</body></html>")
	}
}

func refOf(t *testing.T, tree, role, name string) string {
	t.Helper()
	re := regexp.MustCompile(`\[(ref_\d+)\] ` + role + ` "` + regexp.QuoteMeta(name) + `"`)
	m := re.FindStringSubmatch(tree)
	if m == nil {
		t.Fatalf("no %s %q in tree:\n%s", role, name, tree)
	}
	return m[1]
}

func TestNavigateTree(t *testing.T) {
	b := br(t)
	s := serve(t, html(`<h1>Hello</h1><a href="/docs">Docs</a><button>Save</button><p>plain words</p>`))
	snap, err := b.Navigate(ctx5(t), s.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Status != 200 || snap.Title != "T" || !strings.HasPrefix(snap.URL, s.URL) {
		t.Errorf("snapshot header: %+v", snap)
	}
	for _, want := range []string{`heading "Hello" level=1`, `link "Docs" → /docs`, `button "Save"`, `plain words`} {
		if !strings.Contains(snap.Tree, want) {
			t.Errorf("tree missing %q:\n%s", want, snap.Tree)
		}
	}
	refOf(t, snap.Tree, "button", "Save")
	inter, err := b.ReadPage(ctx5(t), true)
	if err != nil || strings.Contains(inter, "Hello") || !strings.Contains(inter, "Docs") {
		t.Errorf("interactive tree: %v\n%s", err, inter)
	}
	if b.URL() != snap.URL {
		t.Errorf("URL() = %q, want %q", b.URL(), snap.URL)
	}
	if _, err := b.Navigate(ctx5(t), deadURL(t)+"/"); err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("refused navigation error: %v", err)
	}
	if _, err := b.Click(ctx5(t), "ref_999"); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Errorf("unknown ref error: %v", err)
	}
}

func TestClickAndType(t *testing.T) {
	b := br(t)
	s := serve(t, html(`<button id=b onclick="document.getElementById('out').textContent='clicked!'">Go</button>
<div id=out>idle</div><label>Email <input type=text name=email></label>
<input type=text aria-label="Search" onkeydown="if(event.key==='Enter')document.getElementById('out').textContent='enter:'+this.value">`))
	snap, err := b.Navigate(ctx5(t), s.URL)
	if err != nil {
		t.Fatal(err)
	}
	snap, err = b.Click(ctx5(t), refOf(t, snap.Tree, "button", "Go"))
	if err != nil || !strings.Contains(snap.Tree, "clicked!") {
		t.Fatalf("click: %v\n%s", err, snap.Tree)
	}
	snap, err = b.Type(ctx5(t), refOf(t, snap.Tree, "textbox", "Email"), "a@b.co", true, false)
	if err != nil || !strings.Contains(snap.Tree, `value="a@b.co"`) {
		t.Fatalf("type: %v\n%s", err, snap.Tree)
	}
	// clear replaces, no clear appends
	snap, err = b.Type(ctx5(t), refOf(t, snap.Tree, "textbox", "Email"), "x", true, false)
	if err != nil || !strings.Contains(snap.Tree, `value="x"`) {
		t.Fatalf("clear: %v\n%s", err, snap.Tree)
	}
	snap, err = b.Type(ctx5(t), refOf(t, snap.Tree, "textbox", "Email"), "yz", false, false)
	if err != nil || !strings.Contains(snap.Tree, `value="xyz"`) {
		t.Fatalf("append: %v\n%s", err, snap.Tree)
	}
	snap, err = b.Type(ctx5(t), refOf(t, snap.Tree, "textbox", "Search"), "q", true, true)
	if err != nil || !strings.Contains(snap.Tree, "enter:q") {
		t.Fatalf("enter: %v\n%s", err, snap.Tree)
	}
}

func TestPasswordRefused(t *testing.T) {
	b := br(t)
	s := serve(t, html(`<form><input type=text aria-label="User"><input type=password aria-label="Pass" value="hunter2">
<input type=text aria-label="Code" autocomplete="cc-number"><input type=text aria-label="Plain" name="cardnumber"></form>`))
	snap, err := b.Navigate(ctx5(t), s.URL)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(snap.Tree, "hunter2") {
		t.Errorf("password value leaked:\n%s", snap.Tree)
	}
	for _, name := range []string{"Pass", "Code", "Plain"} {
		ref := refOf(t, snap.Tree, "textbox", name)
		el, err := b.Inspect(ctx5(t), ref)
		if err != nil || !el.Password {
			t.Errorf("%s: Password=false (%v)", name, err)
		}
		if _, err := b.Type(ctx5(t), ref, "secret", true, false); err == nil || !strings.Contains(err.Error(), "never types into password") {
			t.Errorf("%s: Type error = %v", name, err)
		}
	}
	el, err := b.Inspect(ctx5(t), refOf(t, snap.Tree, "textbox", "User"))
	if err != nil || el.Password || !el.InForm {
		t.Errorf("User: %+v %v", el, err)
	}
}

func TestInspectFlags(t *testing.T) {
	b := br(t)
	s := serve(t, html(`<form><button>Implicit</button><button type=button>Plain</button><input type=submit value="Send">
<input type=file aria-label="Upload"></form><button>Loose</button>
<a href="/f.zip" download>Get zip</a><a href="/x?y=1">Other</a>`))
	snap, err := b.Navigate(ctx5(t), s.URL+"/page")
	if err != nil {
		t.Fatal(err)
	}
	get := func(role, name string) *Element {
		el, err := b.Inspect(ctx5(t), refOf(t, snap.Tree, role, name))
		if err != nil {
			t.Fatal(err)
		}
		return el
	}
	if e := get("button", "Implicit"); !e.Submits || !e.InForm {
		t.Errorf("implicit: %+v", e)
	}
	if e := get("button", "Plain"); e.Submits || !e.InForm {
		t.Errorf("plain: %+v", e)
	}
	if e := get("button", "Send"); !e.Submits {
		t.Errorf("send: %+v", e)
	}
	if e := get("button", "Loose"); e.Submits || e.InForm {
		t.Errorf("loose: %+v", e)
	}
	if e := get("button", "Upload"); !e.FileInput {
		// Chrome names the file chooser button after the label.
		t.Errorf("upload: %+v", e)
	}
	if e := get("link", "Get zip"); !e.Download || e.Href != s.URL+"/f.zip" {
		t.Errorf("zip: %+v", e)
	}
	if e := get("link", "Other"); e.Download || e.Href != s.URL+"/x?y=1" {
		t.Errorf("other: %+v", e)
	}
}

func TestConsoleCaptured(t *testing.T) {
	b := br(t)
	s := serve(t, html(`<script>console.log("hi"); console.error("boom", 42); setTimeout(()=>{throw new Error("kaput")},0)</script>`))
	snap, err := b.Navigate(ctx5(t), s.URL)
	if err != nil {
		t.Fatal(err)
	}
	if snap.NewConsoleErrors != 2 {
		t.Errorf("NewConsoleErrors = %d, want 2", snap.NewConsoleErrors)
	}
	errs := b.Console(true, false)
	var gotBoom, gotKaput bool
	for _, e := range errs {
		gotBoom = gotBoom || e.Text == "boom 42"
		gotKaput = gotKaput || strings.Contains(e.Text, "kaput")
	}
	if !gotBoom || !gotKaput {
		t.Errorf("errors: %+v", errs)
	}
	if all := b.Console(false, true); len(all) < 3 {
		t.Errorf("all entries: %+v", all)
	}
	if len(b.Console(false, false)) != 0 {
		t.Error("clear did not empty the buffer")
	}
}

func TestNetworkFailures(t *testing.T) {
	b := br(t)
	deadU := deadURL(t)
	dead := strings.TrimPrefix(deadU, "http://")
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/missing.png":
			http.NotFound(w, r)
		case "/boom":
			http.Error(w, "no", 500)
		default:
			html(`<img src="/missing.png"><script>fetch("/boom"); fetch("`+deadU+`/x").catch(()=>{})</script>`)(w, r)
		}
	})
	snap, err := b.Navigate(ctx5(t), s.URL)
	if err != nil {
		t.Fatal(err)
	}
	if snap.NewFailedRequests < 3 {
		t.Errorf("NewFailedRequests = %d", snap.NewFailedRequests)
	}
	failed := b.Network(true, false, "")
	var png, boom, refused bool
	for _, r := range failed {
		png = png || (strings.HasSuffix(r.URL, "/missing.png") && r.Status == 404)
		boom = boom || (strings.HasSuffix(r.URL, "/boom") && r.Status == 500)
		refused = refused || (strings.Contains(r.URL, dead) && r.Failed != "")
	}
	if !png || !boom || !refused {
		t.Errorf("failed requests: %+v", failed)
	}
	if all := b.Network(false, false, "boom"); len(all) != 1 || all[0].Duration <= 0 {
		t.Errorf("filtered: %+v", all)
	}
}

func TestAllowHostBlocksLink(t *testing.T) {
	b := br(t)
	other := serve(t, html(`<h1>Elsewhere</h1>`))
	s := serve(t, html(`<a href="`+other.URL+`/x">Leave</a><a href="/same">Stay</a>`))
	allowHook.Store(func(h string) bool { return h == strings.TrimPrefix(s.URL, "http://") })
	snap, err := b.Navigate(ctx5(t), s.URL)
	if err != nil {
		t.Fatal(err)
	}
	snap, err = b.Click(ctx5(t), refOf(t, snap.Tree, "link", "Leave"))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Blocked) != 1 || !strings.HasPrefix(snap.Blocked[0], other.URL) {
		t.Errorf("Blocked = %v", snap.Blocked)
	}
	if !strings.HasPrefix(snap.URL, s.URL) || strings.Contains(snap.Tree, "Elsewhere") {
		t.Errorf("page changed: %s\n%s", snap.URL, snap.Tree)
	}
	// Navigate is pre-approved even for a host AllowHost rejects.
	if snap, err = b.Navigate(ctx5(t), other.URL); err != nil || !strings.Contains(snap.Tree, "Elsewhere") {
		t.Errorf("Navigate to other host: %v", err)
	}
	// And same-host links still work.
	if snap, err = b.Navigate(ctx5(t), s.URL); err != nil {
		t.Fatal(err)
	}
	snap, err = b.Click(ctx5(t), refOf(t, snap.Tree, "link", "Stay"))
	if err != nil || !strings.HasSuffix(snap.URL, "/same") || len(snap.Blocked) != 0 {
		t.Errorf("same-host link: %v %+v", err, snap)
	}
}

func TestDownloads(t *testing.T) {
	b := br(t)
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/data.csv" {
			w.Header().Set("Content-Type", "text/csv")
			w.Header().Set("Content-Disposition", `attachment; filename="data.csv"`)
			fmt.Fprint(w, "a,b\n1,2\n")
			return
		}
		html(`<a href="/data.csv">Export</a>`)(w, r)
	})
	snap, err := b.Navigate(ctx5(t), s.URL)
	if err != nil {
		t.Fatal(err)
	}
	snap, err = b.Click(ctx5(t), refOf(t, snap.Tree, "link", "Export"))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Downloads) != 1 || !strings.Contains(snap.Downloads[0], "data.csv (blocked") {
		t.Errorf("denied download: %v", snap.Downloads)
	}
	dir := t.TempDir()
	b.AllowDownloads(dir)
	defer b.AllowDownloads("")
	snap, err = b.Click(ctx5(t), refOf(t, snap.Tree, "link", "Export"))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(snap.Downloads) == 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		snap, _ = b.snapshotFor(t)
	}
	if len(snap.Downloads) != 1 || !strings.Contains(snap.Downloads[0], "saved to") {
		t.Errorf("allowed download: %v", snap.Downloads)
	}
	if _, err := os.Stat(filepath.Join(dir, "data.csv")); err != nil {
		entries, _ := os.ReadDir(dir)
		t.Errorf("file not saved: %v (dir has %d entries)", err, len(entries))
	}
}

// snapshotFor takes a plain snapshot, for polling.
func (b *Browser) snapshotFor(t *testing.T) (*Snapshot, error) {
	t.Helper()
	ctx, done, err := b.begin(ctx5(t))
	if err != nil {
		return nil, err
	}
	defer done()
	return b.snapshot(ctx)
}

func TestSetFiles(t *testing.T) {
	b := br(t)
	s := serve(t, html(`<input type=file id=f aria-label="Doc" onchange="document.getElementById('o').textContent='got '+this.files[0].name"><div id=o>none</div>`))
	snap, err := b.Navigate(ctx5(t), s.URL)
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ref := refOf(t, snap.Tree, "button", "Doc")
	if snap, err = b.SetFiles(ctx5(t), ref, []string{f}); err != nil || !strings.Contains(snap.Tree, "got note.txt") {
		t.Errorf("SetFiles: %v\n%s", err, snap.Tree)
	}
	if _, err := b.SetFiles(ctx5(t), ref, []string{f + ".nope"}); err == nil {
		t.Error("missing file accepted")
	}
}

func TestScreenshotCompare(t *testing.T) {
	b := br(t)
	s := serve(t, html(`<style>body{margin:0}#box{position:absolute;left:100px;top:200px;width:300px;height:150px;background:#2a7}</style>
<div id=box></div><button style="position:absolute;left:600px;top:20px" onclick="document.getElementById('box').style.background='#c33'">Flip</button>`))
	snap, err := b.Navigate(ctx5(t), s.URL+"/p?x=1#frag")
	if err != nil {
		t.Fatal(err)
	}
	first, err := b.Screenshot(ctx5(t), false, true)
	if err != nil || first.Compared != "" || first.Width == 0 {
		t.Fatalf("first shot: %+v %v", first, err)
	}
	if fi, err := os.Stat(filepath.Dir(first.Path)); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("shot dir: %v %v", fi, err)
	}
	second, err := b.Screenshot(ctx5(t), false, true)
	if err != nil || second.Compared != first.Path || second.Changed > 0.0005 {
		t.Fatalf("same page: %+v %v", second, err)
	}
	if _, err = b.Click(ctx5(t), refOf(t, snap.Tree, "button", "Flip")); err != nil {
		t.Fatal(err)
	}
	third, err := b.Screenshot(ctx5(t), false, true)
	if err != nil || third.Changed <= 0 {
		t.Fatalf("after change: %+v %v", third, err)
	}
	want := imageRect(100, 200, 400, 350)
	if !third.Box.In(want.Inset(-3)) || third.Box.Dx() < 250 || third.Box.Dy() < 100 {
		t.Errorf("box %v not around %v", third.Box, want)
	}
	if third.Compared != second.Path {
		t.Errorf("compared against %q, want the latest %q", third.Compared, second.Path)
	}
	full, err := b.Screenshot(ctx5(t), true, false)
	if err != nil || full.Width == 0 {
		t.Errorf("full page: %+v %v", full, err)
	}
}

func TestDiffPNGSizes(t *testing.T) {
	mk := func(w, h int) []byte { return solidPNG(w, h) }
	ch, box, ok := diffPNG(mk(10, 10), mk(10, 20))
	if !ok || ch != 0.5 || box.Dy() != 10 {
		t.Errorf("taller: %v %v %v", ch, box, ok)
	}
	if ch, _, _ := diffPNG(mk(10, 10), mk(10, 10)); ch != 0 {
		t.Errorf("identical: %v", ch)
	}
}

func TestSetHeadlessRoundTrip(t *testing.T) {
	b := br(t)
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("no display for a visible window")
	}
	if testing.Short() {
		t.Skip("slow")
	}
	s := serve(t, html(`<h1>Round trip</h1>`))
	if _, err := b.Navigate(ctx5(t), s.URL+"/rt"); err != nil {
		t.Fatal(err)
	}
	for _, headless := range []bool{false, true} {
		if err := b.SetHeadless(ctx5(t), headless); err != nil {
			t.Fatalf("SetHeadless(%v): %v", headless, err)
		}
		if b.Headless() != headless || !strings.HasSuffix(b.URL(), "/rt") {
			t.Errorf("after SetHeadless(%v): headless=%v url=%q", headless, b.Headless(), b.URL())
		}
		tree, err := b.ReadPage(ctx5(t), false)
		if err != nil || !strings.Contains(tree, "Round trip") {
			t.Errorf("page after relaunch: %v\n%s", err, tree)
		}
	}
}

// deadURL is a localhost address nothing listens on.
func deadURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return "http://" + addr
}
