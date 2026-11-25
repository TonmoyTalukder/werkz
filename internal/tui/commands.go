package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/catalog"
	"github.com/TonmoyTalukder/werkz/internal/config"

	tea "github.com/charmbracelet/bubbletea"
)

var commands = []struct{ Name, Desc string }{
	{"/model", "Choose model & provider"},
	{"/effort", "Set thinking effort (low → max)"},
	{"/fast", "Toggle fast mode (Opus 5.5 / Fable 5.2)"},
	{"/login", "Add or change API keys"},
	{"/fallback", "Choose fallback mode and models (default off) · /fallback models · /fallback status"},
	{"/workspace", "Open a workspace (several folders) · /workspace new · /workspace none"},
	{"/add-dir", "Add a working folder, e.g. /add-dir ~/Desktop/mis-frontend"},
	{"/dirs", "Show working folders"},
	{"/compact", "Summarize the conversation to cut tokens · /compact <focus> · /compact auto on|off|300k"},
	{"/openrouter", "OpenRouter models: /openrouter paid (free + paid) or /openrouter free (default)"},
	{"/cache", "Prompt cache stats · /cache ttl 5m|1h · /cache keepalive on|off|30"},
	{"/context", "Show context window usage"},
	{"/cost", "Show session usage and cost"},
	{"/balance", "Set starting Claude credit, e.g. /balance 100"},
	{"/mode", "Cycle permission mode (or shift+tab)"},
	{"/clear", "Clear conversation"},
	{"/help", "Show help"},
	{"/exit", "Quit"},
}

// ---------------------------------------------------------------- commands

func (m *model) command(line string) tea.Cmd {
	f := strings.Fields(line)
	cmd, args := f[0], f[1:]
	p := m.printS
	pp := func(f func() string) tea.Cmd { return m.push(func(int) string { return f() }) }
	switch cmd {
	case "/exit", "/quit":
		m.saveConfig()
		return tea.Quit
	case "/help":
		return pp(m.helpText)
	case "/clear":
		if m.turn.active {
			return p(renderNotice("warn", "Wait for the current turn to finish (esc to interrupt)."))
		}
		m.eng.Reset()
		m.stats.warmUntil = time.Time{}
		m.stats.ctxUsed = 0
		m.entries = nil
		m.push(m.banner)
		return p(sDim.Render("  (conversation cleared)"))
	case "/model":
		if m.turn.active {
			return p(renderNotice("warn", "Wait for the current turn to finish."))
		}
		m.state = stateModels
		m.picker = newPicker(catalog.All(m.cfg), m.cfg.Provider, m.cfg.Model)
		var cmds []tea.Cmd
		for _, pr := range config.Providers {
			if pr.ID != "anthropic" && m.cfg.Configured(pr.ID) {
				cmds = append(cmds, fetchModels(m.cfg, pr.ID))
			}
		}
		return tea.Batch(append(cmds, m.picker.filter.Focus())...)
	case "/effort":
		mi := m.curModelInfo()
		if len(args) > 0 {
			for _, e := range mi.Efforts {
				if e == args[0] {
					m.cfg.Effort = e
					m.saveConfig()
					return p(renderNotice("info", "Effort set to "+e))
				}
			}
			return p(renderNotice("warn", "Unknown effort. Options: "+strings.Join(mi.Efforts, ", ")))
		}
		m.state = stateEffort
		m.effortIdx = 0
		for i, e := range mi.Efforts {
			if e == m.cfg.Effort {
				m.effortIdx = i
			}
		}
		return nil
	case "/fast":
		mi := m.curModelInfo()
		if mi.FastIn == 0 {
			return p(renderNotice("warn", mi.Label+" has no fast mode. It is available on Opus 5.5 and Fable 5.2."))
		}
		m.cfg.Fast = !m.cfg.Fast
		m.saveConfig()
		if w := m.cacheWarn("Switching fast mode"); w != "" {
			m.printS(renderNotice("warn", w))
		}
		if m.cfg.Fast {
			return p(renderNotice("info", fmt.Sprintf("⚡ Fast mode on · ~2.5× faster output at $%g/$%g per MTok", mi.FastIn, mi.FastOut)))
		}
		return p(renderNotice("info", "Fast mode off"))
	case "/login", "/keys", "/logout":
		if m.turn.active {
			return p(renderNotice("warn", "Wait for the current turn to finish."))
		}
		m.state = stateLogin
		m.login = newLogin(m.cfg, false)
		return m.login.inputs[0].Focus()
	case "/fallback":
		if len(args) == 0 {
			m.fbModeSel = map[string]int{"off": 0, "ask": 1, "auto": 2}[m.cfg.Fallback()]
			m.state = stateFallbackMode
			return nil
		}
		if len(args) == 1 && (args[0] == "ask" || args[0] == "auto" || args[0] == "off") {
			m.cfg.FallbackMode = args[0]
			m.saveConfig()
			return pp(m.fallbackText)
		}
		if len(args) == 1 && args[0] == "status" {
			return pp(m.fallbackText)
		}
		if len(args) == 1 && args[0] == "models" {
			mode := m.cfg.Fallback()
			if mode == "off" {
				mode = "ask"
			}
			return m.openFallbackChain(mode)
		}
		if len(args) > 0 {
			var order []string
			for _, id := range strings.Split(strings.Join(args, ","), ",") {
				id = strings.TrimSpace(id)
				if id != "" && config.ProviderByID(id) != nil {
					order = append(order, id)
				}
			}
			if len(order) == 0 {
				return p(renderNotice("warn", "No known providers in that list."))
			}
			m.cfg.FallbackOrder = order
			m.saveConfig()
		}
		return pp(m.fallbackText)
	case "/workspace", "/ws", "/env":
		if m.turn.active {
			return p(renderNotice("warn", "Wait for the current turn to finish."))
		}
		if len(args) == 0 {
			return m.openWsPicker(false)
		}
		switch arg := strings.Join(args, " "); arg {
		case "new":
			return m.openWsForm(nil)
		case "none", "off", "clear":
			return m.clearWorkspace()
		default:
			if ws := m.cfg.Workspace(arg); ws != nil {
				return m.activateWorkspace(ws)
			}
			return p(renderNotice("warn", "No workspace named "+arg+" · /workspace to list them"))
		}
	case "/add-dir":
		if len(args) == 0 {
			return p(renderNotice("warn", "Usage: /add-dir <folder>"))
		}
		return m.addDir(strings.Join(args, " "))
	case "/dirs":
		return pp(m.dirsText)
	case "/compact":
		if m.turn.active {
			return p(renderNotice("warn", "Wait for the current turn to finish."))
		}
		if len(args) >= 2 && args[0] == "auto" {
			switch args[1] {
			case "off":
				m.cfg.NoAutoCompact = true
			case "on":
				m.cfg.NoAutoCompact = false
			default:
				n, err := strconv.Atoi(strings.TrimSuffix(strings.ToLower(args[1]), "k"))
				if err != nil || n < 20 {
					return p(renderNotice("warn", "Usage: /compact auto on|off|<thousand tokens, e.g. 300k>"))
				}
				m.cfg.NoAutoCompact, m.cfg.CompactAt = false, n*1000
			}
			m.saveConfig()
			at, _ := m.eng.CompactThreshold()
			if m.cfg.NoAutoCompact {
				return p(renderNotice("info", "Auto-compaction off · /compact still works manually"))
			}
			return p(renderNotice("info", fmt.Sprintf("Auto-compaction at ~%s tokens", catalog.HumanTokens(int(at)))))
		}
		if m.eng.HistoryLen() == 0 {
			return p(renderNotice("info", "Nothing to compact yet."))
		}
		if m.stats.ctxUsed > 0 && m.stats.ctxUsed < 10_000 {
			return p(renderNotice("info", fmt.Sprintf("The conversation is only ~%s tokens; compacting wouldn't save anything yet.", catalog.HumanTokens(m.stats.ctxUsed))))
		}
		return m.startCompact(strings.Join(args, " "))
	case "/openrouter":
		if len(args) == 1 && (args[0] == "paid" || args[0] == "free") {
			m.cfg.OpenRouterPaid = args[0] == "paid"
			m.saveConfig()
			var cmds []tea.Cmd
			if m.cfg.Configured("openrouter") {
				cmds = append(cmds, fetchModels(m.cfg, "openrouter"), m.fetchCredits())
			}
			msg := "OpenRouter: free models only"
			if m.cfg.OpenRouterPaid {
				msg = "OpenRouter: paid models enabled · they appear in /model with their price and are billed to your OpenRouter credit"
				if m.stats.orCreditsOK {
					msg += fmt.Sprintf(" ($%.2f left)", m.stats.orCredits)
				}
			}
			return tea.Batch(append(cmds, p(renderNotice("info", msg)))...)
		}
		state := "free models only"
		if m.cfg.OpenRouterPaid {
			state = "free and paid models"
		}
		return p(renderNotice("info", "OpenRouter: "+state+" · /openrouter paid shows paid models, /openrouter free hides them"))
	case "/cache":
		switch {
		case len(args) >= 2 && args[0] == "ttl" && (args[1] == "5m" || args[1] == "1h"):
			m.cfg.CacheTTL = args[1]
			if args[1] == "5m" {
				m.cfg.CacheTTL = ""
			}
			m.saveConfig()
		case len(args) >= 2 && args[0] == "keepalive":
			switch args[1] {
			case "off":
				m.cfg.NoKeepAlive = true
				m.eng.StopKeepAlive()
			case "on":
				m.cfg.NoKeepAlive = false
			default:
				n, err := strconv.Atoi(strings.TrimSuffix(args[1], "m"))
				if err != nil || n < 5 || n > 240 {
					return p(renderNotice("warn", "Usage: /cache keepalive on|off|<5-240 minutes>"))
				}
				m.cfg.NoKeepAlive, m.cfg.KeepAliveMin = false, n
			}
			m.saveConfig()
		case len(args) > 0:
			return p(renderNotice("warn", "Usage: /cache · /cache ttl 5m|1h · /cache keepalive on|off|<minutes>"))
		}
		return pp(m.cacheText)
	case "/context":
		return pp(m.contextText)
	case "/cost":
		return pp(m.costText)
	case "/balance":
		if len(args) > 0 {
			v, err := strconv.ParseFloat(strings.TrimPrefix(args[0], "$"), 64)
			if err != nil {
				return p(renderNotice("warn", "Usage: /balance 100"))
			}
			m.cfg.Balance, m.cfg.Spent = v, 0
			m.saveConfig()
		}
		return pp(m.costText)
	case "/mode":
		m.cycleMode()
		return nil
	}
	return p(renderNotice("warn", "Unknown command "+cmd+" · /help"))
}

func (m *model) suggestions() []struct{ Name, Desc string } {
	v := m.input.Value()
	if !strings.HasPrefix(v, "/") || strings.Contains(v, " ") || m.state != stateChat {
		return nil
	}
	var out []struct{ Name, Desc string }
	for _, c := range commands {
		if strings.HasPrefix(c.Name, v) {
			out = append(out, c)
		}
	}
	return out
}
