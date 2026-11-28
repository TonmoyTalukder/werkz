package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/browser"
	"github.com/TonmoyTalukder/werkz/internal/tools"

	tea "github.com/charmbracelet/bubbletea"
)

// /browser: werkz's browser (the user's Chrome with a separate werkz profile).
//
//	/browser          status: which browser, shown or in the background, the page, dev servers
//	/browser show     show its window (to watch what werkz does)
//	/browser hide     run it in the background (the default)
//	/browser close    close it (it starts again when needed)

type browserDoneMsg struct {
	text string
	err  error
}

func (m *model) browserCommand(args []string) tea.Cmd {
	if len(args) == 0 {
		return m.push(func(int) string { return m.browserText() })
	}
	switch strings.ToLower(args[0]) {
	case "show", "visible":
		if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return m.printS(renderNotice("warn", "There's no desktop here (SSH or a console), so the browser can only run in the background."))
		}
		return m.browserAsync(true, "werkz's browser window is shown; werkz works in it as you watch.")
	case "hide", "headless":
		return m.browserAsync(false, "werkz's browser runs in the background.")
	case "view", "see", "look":
		if len(args) > 1 {
			switch strings.ToLower(args[1]) {
			case "auto", "on":
				m.cfg.BrowserView = ""
				m.saveConfig()
				return m.printS(renderNotice("info", "The page shows above the input while werkz uses the browser (ctrl+g: full screen)."))
			case "off":
				m.cfg.BrowserView = "off"
				m.saveConfig()
				m.live.strip = false
				if m.state != stateLive {
					m.unwatch()
				}
				return m.printS(renderNotice("info", "The page no longer opens by itself; /browser view (or ctrl+g) shows it."))
			}
		}
		return m.openLiveView()
	case "connect":
		addr := "" // the Chrome you have open (or one on port 9222)
		if len(args) > 1 {
			addr = args[1]
		}
		if m.turn.active {
			return m.printS(renderNotice("warn", "Wait for the current turn to finish (esc to interrupt)."))
		}
		m.connAddr, m.connSel, m.state, m.dialogAt = addr, 0, stateConnect, time.Now()
		return nil
	case "disconnect":
		if !m.eng.BrowserConnected() {
			return m.printS(renderNotice("info", "werkz isn't connected to your Chrome."))
		}
		m.eng.DisconnectBrowser()
		return m.printS(renderNotice("info", "Disconnected: werkz closed its tab in your Chrome. It uses its own browser again."))
	case "close", "stop":
		m.eng.CloseBrowser()
		return m.printS(renderNotice("info", "Closed werkz's browser. It opens again when werkz needs it."))
	}
	return m.printS(renderNotice("warn", "Usage: /browser · /browser view [auto|off] · /browser connect [port] · /browser disconnect · /browser show · /browser hide · /browser close"))
}

// browserAsync switches the window in the background: relaunching Chrome takes a moment.
func (m *model) browserAsync(visible bool, ok string) tea.Cmd {
	eng := m.eng
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return browserDoneMsg{text: ok, err: eng.SetBrowserVisible(ctx, visible)}
	}
}

func (m *model) handleBrowserDone(msg browserDoneMsg) tea.Cmd {
	if msg.err != nil {
		return m.printS(renderNotice("error", "Browser: "+msg.err.Error()))
	}
	return m.printS(renderNotice("info", msg.text))
}

func (m *model) browserText() string {
	var lines []string
	add := func(s string) { lines = append(lines, indent(wrapTo(s, max(m.w-8, 30)), "  ", "    ")) }
	path, err := browser.Find()
	if err != nil {
		add(sYellow.Render("✗ ") + sText.Render(err.Error()))
	} else {
		running, visible, url := m.eng.BrowserStatus()
		state := "not open yet (opens when werkz needs it)"
		if running {
			state = map[bool]string{true: "open, window shown", false: "open in the background"}[visible]
		}
		add(sGreen.Render("✓ ") + sText.Render(tildify(path)) + sDim.Render(" · "+state))
		if url != "" && url != "about:blank" {
			add(sDim.Render("page: ") + sText.Render(url))
		}
	}
	if servers := tools.DevServers(); len(servers) > 0 {
		lines = append(lines, "")
		for _, s := range servers {
			st := "running"
			if !s.Running() {
				st = s.ExitInfo()
			}
			add(fmt.Sprintf("%s %s %s", sSky.Render(fmt.Sprintf("dev server #%d", s.ID)), sText.Render(s.URL), sDim.Render("· "+s.Command+" · "+st)))
		}
	}
	lines = append(lines, "")
	if m.eng.BrowserConnected() {
		add(sRed.Render("● connected to your Chrome at "+m.eng.BrowserAddr()) + sDim.Render(" · werkz uses only its own tab · /browser disconnect"))
	}
	view := "shows by itself while werkz browses"
	if !m.liveOn() {
		view = "off: ctrl+g or /browser view shows it"
	}
	add(sDim.Render("live view: ") + sText.Render(view) + sDim.Render(" · drawn with "+m.viewMethod()))
	if h := m.img.caps.Hint; h != "" {
		add(sYellow.Render(h))
	}
	add(sDim.Render("/browser view (full screen; auto|off) · /browser connect (use your own Chrome) · /browser show (its window) · /browser hide · /browser close"))
	add(sDim.Render("Pages on this machine open freely; other sites ask. Sending forms (posts), uploads and downloads always ask, and password fields are never typed into."))
	return box(gradient("Browser", 0)+"\n"+strings.Join(lines, "\n"), cSky, m.w)
}

// ---------------------------------------------------------------- /browser connect

type connectDoneMsg struct {
	addr string
	err  error
}

func (m *model) connectKey(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "up", "down", "tab", "shift+tab":
		m.connSel = 1 - m.connSel
	case "esc":
		m.state = stateChat
	case "enter":
		m.state = stateChat
		if m.connSel != 1 {
			return nil
		}
		eng, addr := m.eng, m.connAddr
		note := m.printS(renderNotice("info", "Connecting… if Chrome asks whether to allow remote debugging, click Allow there."))
		return tea.Batch(note, func() tea.Msg {
			// Chrome asks you to allow werkz; that can take a moment
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			return connectDoneMsg{addr: addr, err: eng.ConnectBrowser(ctx, addr)}
		})
	}
	return nil
}

func (m *model) handleConnectDone(msg connectDoneMsg) tea.Cmd {
	if msg.err == nil {
		return m.printS(renderNotice("info", "Connected to "+chromeAddr(msg.addr)+". werkz opened its own window there "+
			"and uses only it; /browser disconnect closes it."))
	}
	if msg.addr == "" && errors.Is(msg.err, browser.ErrNoActiveChrome) {
		return tea.Batch(
			m.printS(renderNotice("info", "To let werkz use the Chrome you have open: in Chrome, go to "+browser.RemoteDebuggingPage+
				" (copied to your clipboard), tick \"Allow remote debugging for this browser instance\", then run /browser connect again. "+
				"Chrome asks you to allow werkz when it connects. This needs Chrome 144 or later; for an older Chrome, "+
				"see /browser connect in the docs.")),
			copyText("the Chrome page", browser.RemoteDebuggingPage))
	}
	if errors.Is(msg.err, browser.ErrChromeDeclined) || errors.Is(msg.err, browser.ErrChromeNoAnswer) {
		what := "Cancel was clicked"
		if errors.Is(msg.err, browser.ErrChromeNoAnswer) {
			what = "nobody answered it in time"
		}
		return m.printS(renderNotice("info", "Chrome asked whether to allow remote debugging and "+what+", so werkz isn't connected. "+
			"Run /browser connect again and click Allow in Chrome."))
	}
	reason := strings.TrimRight(strings.TrimSpace(msg.err.Error()), ".:")
	command := browser.LaunchCommand()
	if port := chromePort(msg.addr); port != "9222" {
		command = strings.Replace(command, "--remote-debugging-port=9222", "--remote-debugging-port="+port, 1)
	}
	return tea.Batch(
		m.printS(renderNotice("error", "Couldn't connect to "+chromeAddr(msg.addr)+": "+reason+".")),
		m.printS(renderNotice("info", "Easiest: in the Chrome you have open, go to "+browser.RemoteDebuggingPage+", tick \"Allow remote "+
			"debugging for this browser instance\" and run /browser connect with no port. Or start a Chrome with this command "+
			"(copied to your clipboard; a separate profile) and connect again:")),
		m.printS("  "+sText.Render(command)),
		copyText("the Chrome command", command))
}

// chromeAddr and chromePort read /browser connect's argument: none, a port, host:port or a URL.
func chromeAddr(addr string) string {
	if strings.TrimSpace(addr) == "" {
		return "your Chrome"
	}
	return "your Chrome at " + chromeHostPort(addr)
}

func chromeHostPort(addr string) string {
	a := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(addr), "http://"), "ws://")
	a = strings.TrimSuffix(a, "/")
	if !strings.Contains(a, ":") {
		a = "127.0.0.1:" + a
	}
	return a
}

func chromePort(addr string) string {
	if strings.TrimSpace(addr) == "" {
		return "9222"
	}
	a := chromeHostPort(addr)
	return a[strings.LastIndex(a, ":")+1:]
}

func (m *model) connectView() string {
	var b strings.Builder
	b.WriteString(sRed.Bold(true).Render("Connect werkz to your own Chrome?") + "\n\n")
	b.WriteString(wrapTo(sText.Render("werkz will open a window in "+chromeAddr(m.connAddr)+", where you are signed in to sites, "+
		"and use only that window's tabs: it never reads your other tabs. Sites other than this machine ask first (even in bypass), "+
		"sending forms, uploads and downloads always ask, and password and payment fields are never typed into."), max(m.w-8, 20)) + "\n")
	b.WriteString(sDim.Render("It lasts until /browser disconnect or you quit werkz.") + "\n\n")
	opts := []string{"No (esc)", "Yes, connect to my Chrome for this session"}
	for i, o := range opts {
		if i == m.connSel {
			b.WriteString(sPink.Render("❯ ") + sPink.Bold(true).Render(o) + "\n")
		} else {
			b.WriteString(sDim.Render("  "+o) + "\n")
		}
	}
	b.WriteString(sDim.Render("↑/↓ choose · enter confirm"))
	return box(b.String(), cRed, m.w)
}
