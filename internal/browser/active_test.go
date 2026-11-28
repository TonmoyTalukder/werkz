package browser

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func writeActivePort(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "DevToolsActivePort"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestParseActivePort(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	writeActivePort(t, good, "9222\n/devtools/browser/0b1c2d3e-aaaa-bbbb\n")
	port, path, err := parseActivePort(filepath.Join(good, "DevToolsActivePort"))
	if err != nil || port != 9222 || path != "/devtools/browser/0b1c2d3e-aaaa-bbbb" {
		t.Fatalf("good file: %d %q %v", port, path, err)
	}
	for name, body := range map[string]string{
		"empty":     "",
		"one line":  "9222\n",
		"bad port":  "nine\n/devtools/browser/x\n",
		"port 0":    "0\n/devtools/browser/x\n",
		"too big":   "70000\n/devtools/browser/x\n",
		"page path": "9222\n/devtools/page/x\n",
		"no id":     "9222\n/devtools/browser/\n",
		"query":     "9222\n/devtools/browser/x?y=1\n",
		"traversal": "9222\n/devtools/browser/../x\n",
		"host":      "9222\nws://evil.com/devtools/browser/x\n",
	} {
		d := filepath.Join(dir, strings.ReplaceAll(name, " ", "-"))
		writeActivePort(t, d, body)
		if _, _, err := parseActivePort(filepath.Join(d, "DevToolsActivePort")); err == nil {
			t.Errorf("%s: accepted %q", name, body)
		}
	}
}

func TestFindActiveChrome(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	live := l.Addr().(*net.TCPAddr).Port
	dead, _ := strconv.Atoi(strings.TrimPrefix(deadURL(t), "http://127.0.0.1:"))

	root := t.TempDir()
	older, newer, stale, missing := filepath.Join(root, "older"), filepath.Join(root, "newer"), filepath.Join(root, "stale"), filepath.Join(root, "missing")
	writeActivePort(t, older, strconv.Itoa(live)+"\n/devtools/browser/old\n")
	writeActivePort(t, newer, strconv.Itoa(live)+"\n/devtools/browser/new\n")
	writeActivePort(t, stale, strconv.Itoa(dead)+"\n/devtools/browser/gone\n") // Chrome quit
	past := time.Now().Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(older, "DevToolsActivePort"), past, past)

	got := findActiveChrome([]string{missing, stale, older, newer}, 0)
	if len(got) != 2 {
		t.Fatalf("found %d, want 2 (the stale file skipped): %+v", len(got), got)
	}
	if want := "ws://127.0.0.1:" + strconv.Itoa(live) + "/devtools/browser/new"; got[0].WS != want || got[0].Profile != newer {
		t.Errorf("newest first: got %+v, want %s", got[0], want)
	}
	if got := findActiveChrome([]string{older, newer}, live+1); len(got) != 0 {
		t.Errorf("port filter kept %+v", got)
	}
	if got := findActiveChrome([]string{older}, live); len(got) != 1 || got[0].Port != live {
		t.Errorf("port filter: %+v", got)
	}
}

func TestUserDataDirs(t *testing.T) {
	has := func(dirs []string, want string) bool {
		for _, d := range dirs {
			if d == want {
				return true
			}
		}
		return false
	}
	linux := userDataDirs("linux", "/home/me", "/home/me/.config", "")
	for _, want := range []string{"/home/me/.config/google-chrome", "/home/me/.config/chromium", "/home/me/.config/BraveSoftware/Brave-Browser",
		"/home/me/.var/app/com.google.Chrome/config/google-chrome", "/home/me/.config/werkz/chrome-connect"} {
		if !has(linux, filepath.FromSlash(want)) {
			t.Errorf("linux lacks %s", want)
		}
	}
	if !has(userDataDirs("linux", "/home/me", "", ""), filepath.FromSlash("/home/me/.config/google-chrome")) {
		t.Error("linux without a config dir")
	}
	mac := userDataDirs("darwin", "/Users/me", "/Users/me/Library/Application Support", "")
	if !has(mac, filepath.FromSlash("/Users/me/Library/Application Support/Google/Chrome")) {
		t.Errorf("mac: %v", mac)
	}
	win := userDataDirs("windows", `C:\Users\me`, `C:\Users\me\AppData\Roaming`, `C:\Users\me\AppData\Local`)
	if !has(win, filepath.Join(`C:\Users\me\AppData\Local`, `Google\Chrome`, "User Data")) {
		t.Errorf("windows: %v", win)
	}
}
