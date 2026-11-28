package browser

import (
	"bufio"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Since Chrome 144, a Chrome the user already has open can allow apps to control it: in
// chrome://inspect/#remote-debugging the user ticks "Allow remote debugging for this browser
// instance". Chrome then listens on 127.0.0.1 and writes the address to a DevToolsActivePort file
// in its profile folder, and asks the user to allow each app that connects. It answers no HTTP
// discovery (/json/version is 404), so the address is read from that file.

// RemoteDebuggingPage is where the user allows werkz to connect to the Chrome they have open.
const RemoteDebuggingPage = "chrome://inspect/#remote-debugging"

// ErrNoActiveChrome means no open Chrome allows remote debugging.
var ErrNoActiveChrome = errors.New("no open Chrome allows remote debugging")

// ErrChromeDeclined means the user answered Cancel when Chrome asked whether to allow werkz.
var ErrChromeDeclined = errors.New("remote debugging was not allowed: Cancel was clicked in Chrome")

// ErrChromeNoAnswer means Chrome's question whether to allow werkz got no answer in time.
var ErrChromeNoAnswer = errors.New("remote debugging was not allowed: Chrome's question got no answer in time")

// activeChrome is a running Chrome found through its DevToolsActivePort file.
type activeChrome struct {
	Profile string // the user data folder
	WS      string // ws://127.0.0.1:port/devtools/browser/…
	Port    int
	at      time.Time
}

// userDataDirs lists the user data folders of Chrome and the browsers built on it, for goos.
// werkz's own profile for the command-line way (LaunchCommand) is included.
func userDataDirs(goos, home, configDir, localAppData string) []string {
	var dirs []string
	switch goos {
	case "windows":
		if localAppData == "" {
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		for _, d := range []string{`Google\Chrome`, `Google\Chrome Beta`, `Google\Chrome Dev`, `Google\Chrome SxS`,
			`Chromium`, `Microsoft\Edge`, `Microsoft\Edge Beta`, `Microsoft\Edge Dev`, `BraveSoftware\Brave-Browser`, `Vivaldi`} {
			dirs = append(dirs, filepath.Join(localAppData, d, "User Data"))
		}
	case "darwin":
		as := filepath.Join(home, "Library", "Application Support")
		for _, d := range []string{"Google/Chrome", "Google/Chrome Beta", "Google/Chrome Dev", "Google/Chrome Canary",
			"Chromium", "Microsoft Edge", "Microsoft Edge Beta", "Microsoft Edge Dev", "BraveSoftware/Brave-Browser", "Vivaldi"} {
			dirs = append(dirs, filepath.Join(as, filepath.FromSlash(d)))
		}
	default:
		if configDir == "" {
			configDir = filepath.Join(home, ".config")
		}
		for _, d := range []string{"google-chrome", "google-chrome-beta", "google-chrome-unstable", "chromium",
			"microsoft-edge", "microsoft-edge-beta", "microsoft-edge-dev", "BraveSoftware/Brave-Browser", "vivaldi"} {
			dirs = append(dirs, filepath.Join(configDir, filepath.FromSlash(d)))
		}
		dirs = append(dirs, // Flatpak and Snap installs keep their profiles elsewhere
			filepath.Join(home, ".var/app/com.google.Chrome/config/google-chrome"),
			filepath.Join(home, ".var/app/org.chromium.Chromium/config/chromium"),
			filepath.Join(home, ".var/app/com.brave.Browser/config/BraveSoftware/Brave-Browser"),
			filepath.Join(home, ".var/app/com.microsoft.Edge/config/microsoft-edge"),
			filepath.Join(home, "snap/chromium/common/chromium"))
	}
	if configDir != "" {
		dirs = append(dirs, filepath.Join(configDir, "werkz", "chrome-connect"))
	}
	return dirs
}

// parseActivePort reads a DevToolsActivePort file: the port on the first line, the browser's
// socket path on the second. Anything else is refused.
func parseActivePort(path string) (port int, wsPath string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 512), 512)
	var lines []string
	for sc.Scan() && len(lines) < 2 {
		lines = append(lines, strings.TrimSpace(sc.Text()))
	}
	if len(lines) < 2 {
		return 0, "", errors.New("DevToolsActivePort is incomplete")
	}
	port, err = strconv.Atoi(lines[0])
	if err != nil || port < 1 || port > 65535 {
		return 0, "", errors.New("DevToolsActivePort has no valid port")
	}
	id, ok := strings.CutPrefix(lines[1], "/devtools/browser/")
	if !ok || id == "" || strings.ContainsAny(id, "/?#%\\ ") {
		return 0, "", errors.New("DevToolsActivePort has no browser socket path")
	}
	return port, lines[1], nil
}

// findActiveChrome lists the open Chromes that allow remote debugging, newest first. A file left
// behind by a Chrome that has quit is skipped: its port no longer answers. port, when not 0, keeps
// only a Chrome on that port.
func findActiveChrome(dirs []string, port int) []activeChrome {
	var found []activeChrome
	seen := map[string]bool{}
	for _, d := range dirs {
		file := filepath.Join(d, "DevToolsActivePort")
		st, err := os.Stat(file)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		p, path, err := parseActivePort(file)
		if err != nil || (port != 0 && p != port) {
			continue
		}
		hostport := net.JoinHostPort("127.0.0.1", strconv.Itoa(p))
		ws := "ws://" + hostport + path
		if seen[ws] {
			continue
		}
		c, err := net.DialTimeout("tcp", hostport, 500*time.Millisecond)
		if err != nil {
			continue // Chrome quit, or turned remote debugging off
		}
		_ = c.Close()
		seen[ws] = true
		found = append(found, activeChrome{Profile: d, WS: ws, Port: p, at: st.ModTime()})
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].at.After(found[j].at) })
	return found
}

// activeChromes looks in this computer's browser profile folders.
func activeChromes(port int) []activeChrome {
	home, _ := os.UserHomeDir()
	cfg, _ := os.UserConfigDir() // werkz's own connect profile lives under it (see LaunchCommand)
	return findActiveChrome(userDataDirs(runtime.GOOS, home, cfg, os.Getenv("LOCALAPPDATA")), port)
}
