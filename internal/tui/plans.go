package tui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/agent"
	"github.com/TonmoyTalukder/werkz/internal/catalog"
	"github.com/TonmoyTalukder/werkz/internal/chatgpt"
	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/llm"

	tea "github.com/charmbracelet/bubbletea"
)

// Subscriptions: the ChatGPT plan (Sign in with ChatGPT) and Claude Code (the user's own
// installed `claude`, signed in to their Claude subscription).

type chatgptURLMsg struct {
	URL    string
	result chan chatgptLoginMsg
}

type chatgptLoginMsg struct {
	Email string
	Err   error
}

type chatgptLogoutMsg struct{ Err error }

// planCommand handles "/login chatgpt", "/logout chatgpt" and "/login claudecode".
func (m *model) planCommand(cmd, target string) tea.Cmd {
	switch target {
	case "chatgpt":
		if cmd == "/logout" {
			if m.loginCancel != nil {
				m.loginCancel()
			}
			m.printS(renderNotice("info", "Signing out of ChatGPT…"))
			return func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				return chatgptLogoutMsg{Err: chatgpt.Logout(ctx)}
			}
		}
		if m.loginCancel != nil {
			m.loginCancel() // a newer attempt replaces an unfinished one
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		m.loginCancel = cancel
		return startChatGPTLogin(ctx)
	case "claudecode", "claude-code", "claude":
		if p := config.ClaudeCLI(); p != "" {
			return m.printS(renderNotice("info", "Claude Code is installed ("+p+"). werkz runs it with your own Claude "+
				"sign-in: if it isn't signed in yet, run `claude` in a terminal and use /login there. Then pick a "+
				"\"(Claude Code)\" model with /model."))
		}
		return m.printS(renderNotice("warn", "Claude Code isn't installed. Install it from code.claude.com, sign in "+
			"by running `claude`, then pick a \"(Claude Code)\" model with /model."))
	}
	return m.printS(renderNotice("warn", "Unknown sign-in. Use /login chatgpt, /logout chatgpt or /login claudecode."))
}

func startChatGPTLogin(ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		urls := make(chan string, 1)
		result := make(chan chatgptLoginMsg, 1)
		go func() {
			c, err := chatgpt.Login(ctx, openBrowser, func(u string) { urls <- u })
			r := chatgptLoginMsg{Err: err}
			if c != nil {
				r.Email = c.Email
			}
			result <- r
		}()
		select {
		case u := <-urls:
			return chatgptURLMsg{URL: u, result: result}
		case r := <-result:
			return r
		}
	}
}

func waitLogin(ch chan chatgptLoginMsg) tea.Cmd { return func() tea.Msg { return <-ch } }

// openBrowser opens a URL in the user's default browser.
func openBrowser(u string) error {
	if !hasBrowser() {
		return errors.New("no browser on this machine")
	}
	var names []string
	switch {
	case runtime.GOOS == "darwin":
		names = []string{"open", "/usr/bin/open"}
	case isWSL():
		names = []string{"wslview", "xdg-open", "/usr/bin/xdg-open"}
	default:
		// a bare PATH (e.g. some app launchers) still finds the system opener
		names = []string{"xdg-open", "/usr/bin/xdg-open", "/usr/local/bin/xdg-open"}
	}
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return exec.Command(p, u).Start()
		}
	}
	return errors.New("no program to open a browser was found")
}

// hasBrowser reports whether a browser can be opened here: not over SSH, and on Linux only with
// a graphical session (or under WSL, where Windows opens it).
func hasBrowser() bool {
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return false
	}
	if runtime.GOOS == "linux" {
		return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "" || isWSL()
	}
	return true
}

func isWSL() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	b, err := os.ReadFile("/proc/version")
	return err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft")
}

// callbackPort is the local port the sign-in returns to (from the sign-in URL's redirect_uri).
func callbackPort(authURL string) string {
	u, err := url.Parse(authURL)
	if err != nil {
		return ""
	}
	r, err := url.Parse(u.Query().Get("redirect_uri"))
	if err != nil {
		return ""
	}
	return r.Port()
}

// signInHelp is what to show when the sign-in link is ready.
func signInHelp(authURL string, browser bool) string {
	if browser {
		return "Opening your browser to sign in with ChatGPT. If it didn't open, visit:\n" + authURL
	}
	port := callbackPort(authURL)
	return "No browser can be opened here (SSH session or no desktop). Open this link in a browser on your " +
		"computer:\n" + authURL + "\nThe sign-in returns to werkz on port " + port + ". If werkz runs on another machine, " +
		"forward that port first by running this on your computer:\nssh -L " + port + ":127.0.0.1:" + port + " <this machine>"
}

func (m *model) handleChatGPTURL(msg chatgptURLMsg) tea.Cmd {
	m.printS(renderNotice("info", signInHelp(msg.URL, hasBrowser())))
	return waitLogin(msg.result)
}

func (m *model) handleChatGPTLogin(msg chatgptLoginMsg) tea.Cmd {
	m.loginCancel = nil
	if msg.Err != nil {
		if strings.Contains(msg.Err.Error(), "context canceled") {
			return nil
		}
		return m.printS(renderNotice("error", "ChatGPT sign-in failed: "+msg.Err.Error()))
	}
	who := ""
	if msg.Email != "" {
		who = " as " + msg.Email
	}
	m.eng.Enable("chatgpt")
	m.printS(renderNotice("info", "Signed in with ChatGPT"+who+". Pick a model marked \"plan\" with /model to use your "+
		"ChatGPT plan; requests count toward its usage limits ("+chatgpt.ManageUsageURL+")."))
	return fetchModels(m.cfg, "chatgpt")
}

func (m *model) handleChatGPTLogout(msg chatgptLogoutMsg) tea.Cmd {
	if msg.Err != nil {
		return m.printS(renderNotice("error", msg.Err.Error()))
	}
	if m.cfg.Provider == "chatgpt" {
		m.ensureProvider()
		m.saveConfig()
	}
	return m.printS(renderNotice("info", "Signed out of ChatGPT."))
}

// planNotice is shown once, the first time a ChatGPT plan model is chosen.
const planNoticeTitle = "You're using your ChatGPT plan"

func planNoticeText() string {
	return "Requests to ChatGPT models now use your ChatGPT plan instead of an API key, and count toward your plan's " +
		"usage limits (on Plus, a five-hour limit shared with your other apps). See or limit werkz's usage at " +
		chatgpt.ManageUsageURL + "."
}

func (m *model) maybePlanNotice(provider string) {
	if provider == "chatgpt" && !m.cfg.PlanNoticeSeen {
		m.cfg.PlanNoticeSeen = true
		m.saveConfig()
		m.state = stateNotice
	}
}

func (m *model) noticeKey(k tea.KeyMsg) {
	switch k.String() {
	case "enter", "esc", " ":
		m.state = stateChat
	}
}

func (m *model) noticeView() string {
	body := sBold.Render(gradient(planNoticeTitle, 0)) + "\n\n" + wrapTo(sText.Render(planNoticeText()), max(m.w-8, 20)) +
		"\n\n" + sPink.Bold(true).Render("❯ Got it") + sDim.Render("  (enter)")
	return box(body, cGreen, m.w)
}

// planStatus is the status-bar text for subscription providers ("" for others).
func (m *model) planStatus(provider string, wide bool) string {
	var s string
	switch provider {
	case "chatgpt":
		s = sGreen.Render("Using ChatGPT plan")
	case "claudecode":
		s = sGreen.Render("Claude subscription")
		if wide {
			s += sDim.Render(" via Claude Code")
		}
	default:
		return ""
	}
	if l, ok := m.planLimits[provider]; ok && len(l.Windows) > 0 {
		var parts []string
		for _, w := range l.Windows {
			parts = append(parts, shortWindow(w))
		}
		s += sDim.Render(" · ") + strings.Join(parts, sDim.Render(" · "))
	} else if provider == "chatgpt" && wide {
		s += sDim.Render(" · Manage usage: " + chatgpt.ManageUsageURL)
	}
	return s
}

// shortWindow is a compact status-bar reading, e.g. "5h 23%" or "weekly Fable 61%".
func shortWindow(w llm.PlanWindow) string {
	label := strings.Replace(w.Label(), "5-hour", "5h", 1)
	switch {
	case w.Limited():
		return sRed.Render(label + " limit reached")
	case w.Used < 0:
		return sDim.Render(label + " ok")
	}
	pct := fmt.Sprintf("%s %d%%", label, int(w.Used*100+0.5))
	switch {
	case w.Used >= 0.9:
		return sRed.Render(pct)
	case w.Used >= 0.75:
		return sYellow.Render(pct)
	}
	return sDim.Render(pct)
}

// handlePlanLimits stores a subscription's latest limits and warns once as a window fills up.
func (m *model) handlePlanLimits(l llm.PlanLimits) tea.Cmd {
	if m.planLimits == nil {
		m.planLimits, m.planWarned = map[string]llm.PlanLimits{}, map[string]bool{}
	}
	m.planLimits[l.Provider] = l
	var cmds []tea.Cmd
	for _, w := range l.Windows {
		level := ""
		switch {
		case w.Limited():
			level = "limit"
		case w.Used >= 0.9:
			level = "90"
		case w.Used >= 0.8:
			level = "80"
		}
		key := l.Provider + "/" + w.Name + "/" + level + "/" + w.ResetsAt.Format(time.RFC3339)
		if level == "" || m.planWarned[key] {
			continue
		}
		m.planWarned[key] = true
		who := map[string]string{"claudecode": "Claude", "chatgpt": "ChatGPT"}[l.Provider]
		msg := fmt.Sprintf("Your %s %s limit is %d%% used%s.", who, w.Label(), int(w.Used*100+0.5), resetText(w.ResetsAt, " and resets "))
		if level == "limit" {
			msg = fmt.Sprintf("Your %s %s limit is used up%s.", who, w.Label(), resetText(w.ResetsAt, "; it resets "))
		}
		cmds = append(cmds, m.printS(renderNotice("warn", msg+" /usage shows all limits.")))
	}
	return tea.Batch(cmds...)
}

// resetText says when a window resets ("" when unknown), with a lead-in such as " and resets ".
func resetText(t time.Time, lead string) string {
	if t.IsZero() {
		return ""
	}
	d := time.Until(t).Round(time.Minute)
	switch {
	case d <= 0:
		return lead + "now"
	case d < 24*time.Hour:
		return lead + "in " + strings.TrimSuffix(d.String(), "0s")
	}
	return lead + t.Local().Format("Mon Jan 2 15:04")
}

// usageText is the /usage panel: each subscription's limits, as far as it reports them.
func (m *model) usageText() string {
	var b strings.Builder
	b.WriteString(gradient("Subscription usage", 0) + "\n")
	shown := false
	for _, p := range []string{"claudecode", "chatgpt"} {
		name := map[string]string{"claudecode": "Claude subscription (Claude Code)", "chatgpt": "ChatGPT plan"}[p]
		l, ok := m.planLimits[p]
		if !ok || len(l.Windows) == 0 {
			if m.cfg.Configured(p) {
				shown = true
				b.WriteString("\n" + sText.Render(name) + "\n")
				if p == "claudecode" {
					b.WriteString(sDim.Render("  Claude Code reports its limits with each request; they appear here after your first one.") + "\n")
				} else {
					b.WriteString(sDim.Render("  OpenAI doesn't share plan usage with apps. See it at "+chatgpt.ManageUsageURL+".") + "\n")
				}
			}
			continue
		}
		shown = true
		b.WriteString("\n" + sText.Render(name) + sDim.Render("  · "+l.Source+", "+l.At.Format("15:04")) + "\n")
		for _, w := range l.Windows {
			line := "  " + padRight(w.Label(), 16)
			switch {
			case w.Used >= 0:
				line += ctxBar(int(w.Used*1000), 1000) + fmt.Sprintf(" %3d%%", int(w.Used*100+0.5))
			case w.Limited():
				line += sRed.Render("used up")
			default:
				line += sDim.Render("within limit")
			}
			line += sDim.Render(resetText(w.ResetsAt, "  · resets "))
			b.WriteString(line + "\n")
		}
		if p == "chatgpt" {
			b.WriteString(sDim.Render("  Details and caps per app: "+chatgpt.ManageUsageURL) + "\n")
		}
	}
	if !shown {
		b.WriteString(sDim.Render("No subscription in use. Use /login chatgpt or a \"(Claude Code)\" model to use one.") + "\n")
	}
	return box(strings.TrimRight(b.String(), "\n"), cSky, m.w)
}

// subscriptionTip points new users at the subscription options they can use but aren't using.
func subscriptionTip(cfg *config.Config) string {
	if cfg.Provider == "chatgpt" || cfg.Provider == "claudecode" {
		return ""
	}
	var opts []string
	if cfg.Configured("claudecode") {
		opts = append(opts, sText.Render("/model")+sDim.Render(" → a \"(Claude Code)\" model uses your Claude subscription"))
	}
	if !cfg.Configured("chatgpt") {
		opts = append(opts, sText.Render("/login chatgpt")+sDim.Render(" uses your ChatGPT plan"))
	} else {
		opts = append(opts, sText.Render("/model")+sDim.Render(" → a \"plan\" model uses your ChatGPT plan"))
	}
	return sDim.Render("Subscriptions: ") + strings.Join(opts, sDim.Render(" · "))
}

// setContextWindow handles "/context window <size>|full".
func (m *model) setContextWindow(args []string) tea.Cmd {
	mi := m.curModelInfo()
	if len(args) == 0 {
		return m.printS(renderNotice("info", "Context window: "+catalog.HumanTokens(m.cfg.Window(mi.Context))+
			". Set a limit with /context window 350k, or /context window full for each model's whole window."))
	}
	switch strings.ToLower(args[0]) {
	case "full", "auto", "off", "max", "default":
		m.cfg.ContextWindow = 0
	default:
		n, err := config.ParseTokens(args[0])
		if err != nil || n < 32_000 {
			return m.printS(renderNotice("warn", "Usage: /context window <size> (at least 32k, e.g. 350k or 1m) or /context window full"))
		}
		m.cfg.ContextWindow = n
	}
	m.saveConfig()
	win := m.cfg.Window(mi.Context)
	if m.stats.ctxMax > 0 || m.stats.ctxUsed == 0 {
		m.stats.ctxMax = win
	}
	msg := "Context window: " + catalog.HumanTokens(win)
	switch {
	case m.cfg.ContextWindow == 0:
		msg += " (each model's full window)"
	case m.cfg.ContextWindow > mi.Context:
		msg = "Context window limit set to " + catalog.HumanTokens(m.cfg.ContextWindow) + "; " + mi.Label +
			" supports " + catalog.HumanTokens(mi.Context) + ", so its full window is used"
	}
	if m.cfg.Provider == "claudecode" {
		if m.cfg.ContextWindow > 0 {
			msg += " · Claude Code gets it as its auto-compact window (at least 100k)"
		}
	} else if !m.cfg.NoAutoCompact {
		at, _ := agent.CompactThreshold(m.cfg, m.curModelInfo().Context)
		msg += " · conversations are summarized at ~" + catalog.HumanTokens(int(at)) + " tokens to stay within it"
	}
	return m.printS(renderNotice("info", msg))
}
