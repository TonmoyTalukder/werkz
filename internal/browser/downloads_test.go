package browser

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// flipJS is what a hostile page can try: every function of its window that is
// not native code, called with true (the way an arm switch would be).
const flipJS = `Object.getOwnPropertyNames(window).forEach(function(n){try{var f=window[n];
 if(typeof f==='function'&&Function.prototype.toString.call(f).indexOf('[native code]')<0)f(true)}catch(e){}});`

const saverJS = `var a=document.createElement('a');a.href=window.URL.createObjectURL(new Blob(['secret'],{type:'text/csv'}));a.download='%s';a.dispatchEvent(new MouseEvent('click'))`

// scriptPage is a page whose scripts download files and submit a form on
// their own, with and without first trying every window function.
func scriptPage(t *testing.T) (*http.ServeMux, *counter) {
	t.Helper()
	c := &counter{hits: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/post", func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.hits[r.Method+" "+r.URL.Path]++
		c.mu.Unlock()
		html("<h1>posted</h1>")(w, r)
	})
	mux.HandleFunc("/", html(`<h1>Export</h1><form id=f method=post action="/post"><input name=x></form>
<button onclick="`+strings.ReplaceAll(strings.Replace(saverJS, "%s", "saver.csv", 1), `"`, "&quot;")+`">Saver</button>
<button onclick="`+strings.ReplaceAll(flipJS, `"`, "&quot;")+strings.ReplaceAll(strings.Replace(saverJS, "%s", "armed.csv", 1), `"`, "&quot;")+`">Flip download</button>
<button onclick="`+strings.ReplaceAll(flipJS, `"`, "&quot;")+`document.getElementById('f').submit()">Flip submit</button>
<button onclick="var a=document.createElement('a');a.href=window.URL.createObjectURL(new Blob(['ok']));a.download='plain.bin';document.body.appendChild(a);a.click()">Clicker</button>`))
	return mux, c
}

func filesNamed(root string, prefixes ...string) []string {
	var found []string
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		for _, pre := range prefixes {
			if strings.HasPrefix(fi.Name(), pre) {
				found = append(found, p)
			}
		}
		return nil
	})
	return found
}

// ownLaunch starts a launch-mode browser of its own with a private HOME.
func ownLaunch(t *testing.T) (*Browser, string) {
	t.Helper()
	if _, err := Find(); err != nil {
		t.Skip("no browser installed: ", err)
	}
	if shared != nil {
		_ = shared.Close()
		shared, sharedErr, sharedOnce = nil, nil, sync.Once{}
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DOWNLOAD_DIR", filepath.Join(home, "Downloads"))
	b, err := Start(ctx5(t), Options{Headless: true, ProfileDir: filepath.Join(home, "prof"), ShotDir: filepath.Join(home, "shots"), AllowHost: func(string) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b, home
}

// scriptDownloads runs the download and submit attacks against b and checks
// that nothing reaches disk without Allow, and that Allow works.
func scriptDownloads(t *testing.T, b *Browser, home string, wantNote bool) {
	t.Helper()
	mux, c := scriptPage(t)
	s := serve(t, mux.ServeHTTP)
	var snap *Snapshot
	var err error
	for _, name := range []string{"Saver", "Flip download", "Clicker"} {
		snap, err = b.Navigate(ctx5(t), s.URL+"/")
		if err != nil {
			t.Fatal(err)
		}
		got, err := b.Click(ctx5(t), refOf(t, snap.Tree, "button", name), Allow{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		t.Logf("%s: %+v", name, got.Downloads)
		if wantNote && !hasNote(got.Downloads, "blocked: downloads are off") {
			t.Errorf("%s: downloads = %v, want a blocked note", name, got.Downloads)
		}
	}
	time.Sleep(1500 * time.Millisecond)
	if f := filesNamed(home, "saver", "armed", "plain"); len(f) != 0 {
		t.Fatalf("a script downloaded without permission: %v", f)
	}
	// a script that flips every switch it can find still cannot submit
	snap, err = b.Navigate(ctx5(t), s.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.Click(ctx5(t), refOf(t, snap.Tree, "button", "Flip submit"), Allow{})
	if err != nil || c.n("POST /post") != 0 || !hasNote(got.Blocked, "form submit to") {
		t.Fatalf("flipping switches submitted a form: %v %+v (POSTs %d)", err, got, c.n("POST /post"))
	}
	// the switch of the page is not on its window at all
	if v, err := b.Eval(ctx5(t), `Object.getOwnPropertyNames(window).filter(function(n){return /^__wz/.test(n)&&typeof window[n]==='function'&&Function.prototype.toString.call(window[n]).indexOf('native code')<0}).length`); err != nil || v != "1" {
		// only the Eval lock switch may remain
		t.Errorf("non-native __wz functions on window = %q, %v; want 1 (Eval lock only)", v, err)
	}
}

func TestConnectedScriptDownloadsBlocked(t *testing.T) {
	u := startUserChrome(t)
	mux, _ := scriptPage(t)
	s := serve(t, mux.ServeHTTP)
	b, err := Connect(ctx5(t), u.port, Options{ShotDir: filepath.Join(t.TempDir(), "shots"), AllowHost: func(string) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	scriptDownloads(t, b, u.home, true)

	// allowed for one action only
	snap, err := b.Navigate(ctx5(t), s.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.Click(ctx5(t), refOf(t, snap.Tree, "button", "Clicker"), Allow{DownloadDir: t.TempDir()})
	if err != nil || !hasNote(got.Downloads, "plain.bin (saved by Chrome") {
		t.Fatalf("allowed download: %v %+v", err, got)
	}
	eventually(t, "Chrome to save the allowed download", 5*time.Second, func() bool { return len(filesNamed(u.home, "plain.bin")) > 0 })
	_ = os.RemoveAll(filepath.Join(u.home, "Downloads"))
	if _, err := b.Click(ctx5(t), refOf(t, snap.Tree, "button", "Saver"), Allow{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	if f := filesNamed(u.home, "saver"); len(f) != 0 {
		t.Errorf("the allowance outlived the action: %v", f)
	}
}

func TestLaunchScriptDownloadsBlocked(t *testing.T) {
	b, home := ownLaunch(t)
	scriptDownloads(t, b, home, false)
	mux, _ := scriptPage(t)
	s := serve(t, mux.ServeHTTP)
	snap, err := b.Navigate(ctx5(t), s.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "dl")
	if _, err := b.Click(ctx5(t), refOf(t, snap.Tree, "button", "Saver"), Allow{DownloadDir: dir}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the allowed download", 5*time.Second, func() bool { return len(filesNamed(dir, "saver")) > 0 })
	if _, err := b.Click(ctx5(t), refOf(t, snap.Tree, "button", "Clicker"), Allow{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	if f := filesNamed(home, "plain"); len(f) != 0 {
		t.Errorf("the allowance outlived the action: %v", f)
	}
}

func TestHasSecretField(t *testing.T) {
	b := br(t)
	cases := []struct {
		name, body string
		want       bool
	}{
		{"plain", `<input type=text name=user><textarea></textarea>`, false},
		{"password", `<input type=password>`, true},
		{"one-time-code", `<input autocomplete="one-time-code">`, true},
		{"card by name", `<input name="card_number">`, true},
		{"iframe", `<iframe srcdoc="<input type=password>"></iframe>`, true},
		{"open shadow", `<div id=h></div><script>document.getElementById('h').attachShadow({mode:'open'}).innerHTML='<input type=password>'</script>`, true},
		{"closed shadow", `<div id=h></div><script>document.getElementById('h').attachShadow({mode:'closed'}).innerHTML='<input autocomplete=cc-csc>'</script>`, true},
		{"harmless iframe", `<iframe srcdoc="<input name=q>"></iframe>`, false},
	}
	for _, tc := range cases {
		s := serve(t, html(tc.body))
		if _, err := b.Navigate(ctx5(t), s.URL+"/"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(200 * time.Millisecond)
		if got, err := b.HasSecretField(ctx5(t)); err != nil || got != tc.want {
			t.Errorf("%s: HasSecretField = %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}
}

// While werkz is connected and idle, the user's own tabs download as usual:
// werkz's download switch is profile-wide, so it is on only during an action.
func TestConnectedUserTabDownloads(t *testing.T) {
	u := startUserChrome(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/user", html(`<button onclick="var a=document.createElement('a');a.href=window.URL.createObjectURL(new Blob(['mine']));a.download='usertab.bin';a.dispatchEvent(new MouseEvent('click'))">Mine</button>`))
	mux.HandleFunc("/", html(`<h1>werkz</h1>`))
	s := serve(t, mux.ServeHTTP)
	user := u.openTab(s.URL + "/user")
	userCtx := u.userContext(user.ID)
	b, err := Connect(ctx5(t), u.port, Options{ShotDir: filepath.Join(t.TempDir(), "shots"), AllowHost: func(string) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err := b.Navigate(ctx5(t), s.URL+"/"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(denyGrace + 500*time.Millisecond) // idle: the deny ends shortly after werkz's last operation
	userEval(t, userCtx, `document.querySelector('button').click()`)
	eventually(t, "the user's own download", 5*time.Second, func() bool { return len(filesNamed(u.home, "usertab.bin")) > 0 })
}

// A cross-site (out-of-process) iframe of werkz's tab cannot download either.
func TestConnectedCrossSiteIframeDownload(t *testing.T) {
	u := startUserChrome(t)
	inner := serve(t, html(`<button style="position:fixed;left:0;top:0;width:200px;height:80px" onclick="var a=document.createElement('a');a.href=window.URL.createObjectURL(new Blob(['x']));a.download='oopif.bin';a.dispatchEvent(new MouseEvent('click'))">In frame</button>`))
	innerURL := strings.Replace(inner.URL, "127.0.0.1", "localhost", 1)
	outer := serve(t, html(`<iframe src="`+innerURL+`/" style="position:absolute;left:0;top:0;width:300px;height:200px;border:0"></iframe>`))
	b, err := Connect(ctx5(t), u.port, Options{ShotDir: filepath.Join(t.TempDir(), "shots"), AllowHost: func(string) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err := b.Navigate(ctx5(t), outer.URL+"/"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	got, err := b.ClickAt(ctx5(t), 50, 40, "left", 1, "", Allow{})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	if f := filesNamed(u.home, "oopif"); len(f) != 0 {
		t.Fatalf("a cross-site iframe downloaded: %v (notes %v)", f, got.Downloads)
	}
	t.Logf("iframe download notes: %v", got.Downloads)
}

func TestGetFormSubmitIsReported(t *testing.T) {
	b := br(t)
	s, c := countingServer(t, `<form id=f method=get action="/search"><input name=q value="secret words"></form>
<button onclick="document.getElementById('f').requestSubmit()">Send</button>`)
	snap, err := b.Navigate(ctx5(t), s.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.Click(ctx5(t), refOf(t, snap.Tree, "button", "Send"), Allow{})
	if err != nil || !hasNote(got.Notes, "sent a form to "+s.URL+"/search?q=secret+words (GET)") || c.n("GET /search") != 1 {
		t.Fatalf("GET form: %v notes=%v blocked=%v hits=%d", err, got.Notes, got.Blocked, c.n("GET /search"))
	}
	if next, err := b.Navigate(ctx5(t), s.URL+"/"); err != nil || len(next.Notes) != 0 {
		t.Errorf("notes leaked into the next call: %v %v", err, next.Notes)
	}
}

// A page that downloads by itself 300 ms after it loads, while werkz opens it
// (Browse, or a new tab), is denied in connected mode, and the note says so.
func TestConnectedOnLoadDownloads(t *testing.T) {
	u := startUserChrome(t)
	mux := http.NewServeMux()
	for _, c := range []struct {
		path string
		size int
	}{{"small", 10}, {"big", 5 << 20}, {"tabsmall", 10}, {"tabbig", 5 << 20}} {
		mux.HandleFunc("/"+c.path, html(fmt.Sprintf(`<h1>%s</h1><script>setTimeout(function(){var a=document.createElement('a');a.href=window.URL.createObjectURL(new Blob([new Uint8Array(%d)]));a.download='onload-%s.bin';a.dispatchEvent(new MouseEvent('click'))},300)</script>`, c.path, c.size, c.path)))
	}
	s := serve(t, mux.ServeHTTP)
	b, err := Connect(ctx5(t), u.port, Options{ShotDir: filepath.Join(t.TempDir(), "shots"), AllowHost: func(string) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	// A slow page may download after the call returned: its note then shows
	// in a later call, so notes are collected across all of them.
	var notes []string
	for _, n := range []string{"small", "big"} {
		// a fresh tab each: Chrome throttles repeated automatic downloads in one tab
		if _, err := b.NewTab(ctx5(t), ""); err != nil {
			t.Fatal(err)
		}
		got, err := b.Navigate(ctx5(t), s.URL+"/"+n)
		if err != nil {
			t.Fatal(err)
		}
		notes = append(notes, got.Downloads...)
	}
	for _, n := range []string{"tabsmall", "tabbig"} {
		got, err := b.NewTab(ctx5(t), s.URL+"/"+n)
		if err != nil {
			t.Fatal(err)
		}
		notes = append(notes, got.Downloads...)
	}
	time.Sleep(2 * time.Second)
	if late, _ := b.snapshotFor(t); late != nil {
		notes = append(notes, late.Downloads...)
	}
	for _, n := range []string{"small", "big", "tabsmall", "tabbig"} {
		if !hasNote(notes, "onload-"+n+".bin (blocked: downloads are off)") {
			t.Errorf("no blocked note for %s in %v", n, notes)
		}
	}
	if f := filesNamed(u.home, "onload-"); len(f) != 0 {
		t.Errorf("an on-load download landed: %v", f)
	}
}

// The live view only looks at the page: capturing frames never turns the user's downloads off.
func TestCaptureLeavesDownloadsAlone(t *testing.T) {
	b := &Browser{connected: true, closed: true}
	if _, _, err := b.beginRead(t.Context()); err == nil {
		t.Fatal("a closed browser accepted an operation")
	}
	b.mu.Lock()
	gen := b.denyGen
	b.mu.Unlock()
	if gen != 0 {
		t.Fatalf("a read-only operation touched the download switch (generation %d)", gen)
	}
	if !b.opMu.TryLock() {
		t.Fatal("the lock wasn't released")
	}
	b.opMu.Unlock()
}

// A download the page starts after werkz's action finished says so; one during an action doesn't.
func TestLateDownloadNotesSayWhen(t *testing.T) {
	b := &Browser{}
	b.mu.Lock()
	b.idle = true // the previous action's results were collected
	late := b.addDownloadLocked("report.csv (blocked: downloads are off)")
	b.mu.Unlock()
	if !strings.Contains(late, "after werkz's previous action had finished") {
		t.Fatalf("late note: %q", late)
	}
	unlock := b.lockOp() // the next action starts
	b.mu.Lock()
	during := b.addDownloadLocked("data.zip (blocked: downloads are off)")
	b.mu.Unlock()
	unlock()
	if strings.Contains(during, "previous action") {
		t.Fatalf("a note during an action was marked late: %q", during)
	}
}

// Chrome's own state changes are retried; real failures are not.
func TestTransientNetError(t *testing.T) {
	for text, want := range map[string]bool{"net::ERR_CERT_VERIFIER_CHANGED": true, "net::ERR_NETWORK_CHANGED": true,
		"net::ERR_NAME_NOT_RESOLVED": false, "net::ERR_CERT_AUTHORITY_INVALID": false, "": false} {
		if transientNetError(text) != want {
			t.Errorf("%q: want %v", text, want)
		}
	}
}
