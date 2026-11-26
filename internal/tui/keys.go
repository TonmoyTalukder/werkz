package tui

import (
	"context"
	"math/rand"
	"strings"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/session"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	ks := k.String()
	if m.sel.on {
		text := m.selectedText()
		m.sel = textSel{}
		m.follow = m.vp.AtBottom()
		if ks == "ctrl+c" || ks == "y" {
			return m, copyText("the selection", text) // copy, as in most apps: not interrupt/quit
		}
		if ks == "esc" {
			return m, nil
		}
	}
	if ks == "ctrl+c" {
		if m.turn.active {
			m.turn.cancel()
			return m, nil
		}
		if m.state != stateChat && m.state != stateLogin {
			m.state = stateChat
			return m, nil
		}
		if m.input.Value() != "" {
			m.input.Reset()
			return m, nil
		}
		if time.Since(m.lastCtrlC) < 1500*time.Millisecond {
			m.saveConfig()
			return m, tea.Quit
		}
		m.lastCtrlC = time.Now()
		m.flash = "Press Ctrl+C again to exit"
		return m, nil
	}
	m.flash, m.toast = "", ""

	switch m.state {
	case stateResume:
		return m, m.resumeKey(k)
	case stateCopy:
		return m, m.copyPickerKey(k)
	case stateSelect:
		return m, m.selectKey(k)
	case stateLive:
		return m, m.liveKey(k)
	case stateLogin:
		done, cmd := m.login.key(k)
		if done {
			m.state = stateChat
			m.cfg.Onboarded = true
			m.ensureProvider(true)
			m.saveConfig()
			m.push(m.banner)
			return m, tea.Batch(cmd, m.runOnStart(), m.fetchCredits(), m.input.Focus())
		}
		return m, cmd
	case stateModels:
		return m, m.pickerKey(k)
	case stateEffort:
		return m, m.effortKey(k)
	case stateNotice:
		m.noticeKey(k)
		return m, nil
	case stateBypass:
		if m.typeahead() {
			return m, nil
		}
		m.bypassKey(k)
		return m, nil
	case statePerm:
		if m.typeahead() {
			return m, nil
		}
		m.permKey(k)
		return m, nil
	case stateWorkspaces:
		return m, m.wsPickerKey(k)
	case stateWsForm:
		return m, m.wsFormKey(k)
	case stateFallback:
		if m.typeahead() {
			return m, nil
		}
		return m, m.fallbackKey(k)
	case stateFallbackMode:
		return m, m.fallbackModeKey(k)
	case stateFallbackChain:
		return m, m.fallbackChainKey(k)
	}

	// chat state
	if rows, cmd := m.mentionSugs(); rows != nil && m.mentionKey(k, rows) {
		return m, cmd
	}
	sugs := m.suggestions()
	switch ks {
	case "ctrl+s":
		return m, m.toggleMouse()
	case "alt+up", "ctrl+o":
		m.startSelect()
		return m, nil
	case "esc":
		if m.turn.active {
			m.turn.cancel()
		}
		return m, nil
	case "shift+tab":
		m.cycleMode()
		return m, nil
	case "up", "down":
		if len(sugs) > 0 {
			if ks == "up" {
				m.sugSel = (m.sugSel - 1 + len(sugs)) % len(sugs)
			} else {
				m.sugSel = (m.sugSel + 1) % len(sugs)
			}
			return m, nil
		}
	case "tab":
		if len(sugs) > 0 {
			m.input.SetValue(sugs[m.sugSel%len(sugs)].Name + " ")
			m.input.CursorEnd()
			return m, nil
		}
	case "enter":
		v := strings.TrimSpace(m.input.Value())
		if len(sugs) > 0 && !strings.Contains(v, " ") && v != sugs[m.sugSel%len(sugs)].Name {
			v = sugs[m.sugSel%len(sugs)].Name
		}
		if v == "" {
			return m, nil
		}
		if !strings.HasPrefix(v, "/") && !m.turn.active {
			if warn := m.idleCheck(v); warn != "" {
				return m, m.printS(renderNotice("warn", warn)) // text stays in the box; Enter again sends
			}
		}
		m.input.Reset()
		m.input.SetHeight(1)
		m.sugSel = 0
		if strings.HasPrefix(v, "/") {
			return m, m.command(v)
		}
		if m.turn.active {
			m.turn.queue = append(m.turn.queue, v)
			return m, nil
		}
		return m, m.submit(v)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	m.input.SetHeight(min(max(m.input.LineCount(), 1), 8))
	if len(m.suggestions()) == 0 {
		m.sugSel = 0
	}
	if _, start, _, ok := m.mentionAt(); !ok || start != m.ment.closed {
		m.ment.closed = -1 // a new mention: the picker may open again
	}
	m.ment.sel = 0
	_, icmd := m.mentionSugs() // starts indexing as soon as "@" is typed
	return m, tea.Batch(cmd, icmd)
}

// cycleMode moves to the next permission mode; the running engine sees it immediately.
func (m *model) cycleMode() {
	next := config.ModeDefault
	for i, md := range modes {
		if md == m.cfg.Mode {
			next = modes[(i+1)%len(modes)]
			break
		}
	}
	m.setMode(next)
}

// setMode switches the permission mode. Bypass needs a confirmation first.
func (m *model) setMode(mode string) {
	if mode == config.ModeBypass && m.cfg.Mode != config.ModeBypass {
		m.state, m.bypassSel = stateBypass, 0
		m.dialogAt = time.Now()
		return
	}
	m.cfg.Mode = mode
	m.eng.SetMode(mode)
}

// bypassKey answers the Bypass confirmation: only an explicit "Yes" turns it on.
func (m *model) bypassKey(k tea.KeyMsg) {
	switch k.String() {
	case "up", "down", "tab", "shift+tab":
		m.bypassSel = 1 - m.bypassSel
	case "esc":
		m.bypassSel = 0
		fallthrough
	case "enter":
		m.state = stateChat
		if m.bypassSel == 1 {
			m.cfg.Mode = config.ModeBypass
		} else {
			m.cfg.Mode = config.ModeDefault
		}
		m.eng.SetMode(m.cfg.Mode)
	}
}

func (m *model) submit(text string) tea.Cmd {
	if m.eng == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.turn.cancel = cancel
	m.turn.active = true
	m.turn.started = time.Now()
	m.turn.approxOut = 0
	m.turn.verb = verbs[rand.Intn(len(verbs))]
	m.turn.live.Reset()
	m.turn.thinking.Reset()
	go m.eng.Run(ctx, text, *m.cfg) // the engine gets its own snapshot of the settings
	m.pushItem(session.Item{Kind: "user", Text: text})
	return tick()
}

// dialogGuard ignores keys for a moment after a permission or fallback dialog opens, so
// keystrokes meant for the message being typed can't answer it.
const dialogGuard = 400 * time.Millisecond

func (m *model) typeahead() bool { return time.Since(m.dialogAt) < dialogGuard }
