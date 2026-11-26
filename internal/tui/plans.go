package tui

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/chatgpt"
	"github.com/TonmoyTalukder/werkz/internal/config"

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
func planStatus(provider string, wide bool) string {
	switch provider {
	case "chatgpt":
		if wide {
			return sGreen.Render("Using ChatGPT plan") + sDim.Render(" · Manage usage: "+chatgpt.ManageUsageURL)
		}
		return sGreen.Render("Using ChatGPT plan")
	case "claudecode":
		return sGreen.Render("Claude subscription") + sDim.Render(" via Claude Code")
	}
	return ""
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
