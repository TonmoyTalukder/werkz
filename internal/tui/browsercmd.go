package tui

import (
	"context"
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
	case "close", "stop":
		m.eng.CloseBrowser()
		return m.printS(renderNotice("info", "Closed werkz's browser. It opens again when werkz needs it."))
	}
	return m.printS(renderNotice("warn", "Usage: /browser · /browser show · /browser hide · /browser close"))
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
	add(sDim.Render("/browser show · /browser hide · /browser close"))
	add(sDim.Render("Pages on this machine open freely; other sites ask. Form submits, uploads and downloads always ask, and password fields are never typed into."))
	return box(gradient("Browser", 0)+"\n"+strings.Join(lines, "\n"), cSky, m.w)
}
