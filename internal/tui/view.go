package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/catalog"
	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/textutil"

	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------------- view

func (m *model) View() string {
	if m.state == stateLogin {
		return clipLines(m.login.view(m.w, m.h), m.w, m.h)
	}
	bottom := clipLines(m.bottomView(), m.w, 0)
	if lines := strings.Split(bottom, "\n"); len(lines) >= m.h {
		// tiny window: the dialog/input takes the whole screen, top part first
		return strings.Join(lines[:m.h], "\n")
	}
	m.vp.Width = m.w
	m.vp.Height = max(m.h-lipgloss.Height(bottom), 1)
	content := m.history(m.w)
	if live := m.liveView(); live != "" {
		content += "\n" + live
	}
	m.vp.SetContent(clipLines(content, m.w, 0))
	if m.follow {
		m.vp.GotoBottom()
	}
	top := m.vp.View()
	if !m.follow && !m.vp.AtBottom() {
		// hint that there is newer output below
		lines := strings.Split(top, "\n")
		lines[len(lines)-1] = lipgloss.PlaceHorizontal(m.w, lipgloss.Right, sPink.Render(" ↓ new output · pgdn / end "))
		top = strings.Join(lines, "\n")
	}
	return top + "\n" + bottom
}

// liveView is the in-progress part of the turn, shown at the end of the transcript.
func (m *model) liveView() string {
	if !m.turn.active {
		return ""
	}
	var b strings.Builder
	if t := strings.TrimSpace(m.turn.thinking.String()); t != "" && m.turn.live.Len() == 0 {
		b.WriteString(tail(renderThinkingLive(t, m.w), 6) + "\n")
	}
	if t := m.turn.live.String(); t != "" {
		b.WriteString(indent(wrapTo(t, max(m.w-2, 10)), sViolet.Render("⏺ "), "  ") + "\n")
	}
	if m.turn.pending != nil {
		dot := sYellow.Render("⏺")
		if m.turn.frame%6 < 3 {
			dot = sFaint.Render("⏺")
		}
		b.WriteString(dot + " " + sBold.Render(m.turn.pending.Name) + sDim.Render("("+m.turn.pending.Summary+")") + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// bottomView is the fixed area under the transcript: spinner, input or dialog, status bar.
func (m *model) bottomView() string {
	var b strings.Builder
	if m.turn.active {
		b.WriteString("\n" + m.spinnerLine() + "\n")
		for _, q := range m.turn.queue {
			b.WriteString(sDim.Render("  ↳ queued: "+textutil.Truncate(q, max(m.w-16, 10))) + "\n")
		}
	}
	switch m.state {
	case stateModels:
		b.WriteString(m.pickerView())
	case stateEffort:
		b.WriteString(m.effortView())
	case stateNotice:
		b.WriteString(m.noticeView())
	case statePerm:
		b.WriteString(m.permView())
	case stateWorkspaces:
		b.WriteString(m.wsPickerView())
	case stateWsForm:
		b.WriteString(m.wsFormView())
	case stateFallback:
		b.WriteString(m.fallbackView())
	case stateFallbackMode:
		b.WriteString(m.fallbackModeView())
	case stateFallbackChain:
		b.WriteString(m.fallbackChainView())
	default:
		if !m.turn.active {
			b.WriteString(m.effortHint() + "\n")
		}
		rule := sFaint.Render(strings.Repeat("─", max(m.w, 10)))
		if m.cfg.Mode == config.ModeAuto {
			rule = gradient(strings.Repeat("─", max(m.w, 10)), float64(m.turn.frame)/40)
		}
		b.WriteString(rule + "\n" + m.input.View() + "\n" + rule + "\n")
		if sugs := m.suggestions(); len(sugs) > 0 {
			for i, s := range sugs {
				if i == m.sugSel%len(sugs) {
					b.WriteString(sPink.Render("❯ "+padRight(s.Name, 12)) + sText.Render(s.Desc) + "\n")
				} else {
					b.WriteString(sDim.Render("  "+padRight(s.Name, 12)+s.Desc) + "\n")
				}
			}
		}
	}
	b.WriteString(m.statusBar())
	return b.String()
}

func (m *model) spinnerLine() string {
	sp := gradient(spinnerFrames[m.turn.frame%len(spinnerFrames)], float64(m.turn.frame)/10)
	verb := m.turn.verb
	switch {
	case m.turn.pending != nil:
		verb = "Running " + m.turn.pending.Name
	case m.turn.prepTool != "":
		verb = "Preparing " + m.turn.prepTool
	case m.turn.thinking.Len() > 0 && m.turn.live.Len() == 0:
		verb = "Thinking"
	}
	el := int(time.Since(m.turn.started).Seconds())
	info := fmt.Sprintf(" (%ds · ↓ %s tokens · via %s · esc to interrupt)", el, catalog.HumanTokens(m.turn.approxOut), m.turn.curProv)
	return sp + " " + gradient(verb+"…", float64(m.turn.frame)/25) + sDim.Render(info)
}

func (m *model) effortHint() string {
	right := sDim.Render("◐ " + m.cfg.Effort + " · /effort")
	if m.cfg.Fast {
		right = sYellow.Render("⚡ fast ") + right
	}
	return lipgloss.PlaceHorizontal(m.w, lipgloss.Right, right)
}

func ctxBar(used, total int) string {
	if total <= 0 {
		return ""
	}
	pct := float64(used) / float64(total)
	cells := 10
	filled := int(pct*float64(cells) + 0.5)
	if used > 0 && filled == 0 {
		filled = 1
	}
	c := cGreen
	if pct > 0.8 {
		c = cRed
	} else if pct > 0.5 {
		c = cYellow
	}
	return fg(c).Render(strings.Repeat("▰", min(filled, cells))) + sFaint.Render(strings.Repeat("▱", max(cells-filled, 0))) +
		sDim.Render(fmt.Sprintf(" %d%% of %s", int(pct*100), catalog.HumanTokens(total)))
}

func cwdLabel() string {
	wd, _ := os.Getwd()
	h, _ := os.UserHomeDir()
	if wd == h {
		return "~"
	}
	return filepath.Base(wd)
}

func (m *model) statusBar() string {
	mi := m.curModelInfo()
	sep := sFaint.Render(" │ ")
	model := sSky.Bold(true).Render(mi.Label) + " " + sViolet.Render("["+m.cfg.Effort+"]")
	if m.cfg.Fast {
		model += sYellow.Render(" ⚡")
	}
	ctxMax := m.stats.ctxMax
	if ctxMax == 0 {
		ctxMax = m.cfg.Window(mi.Context)
	}
	ctxFull := ctxBar(m.stats.ctxUsed, ctxMax)
	ctxShort := sDim.Render(fmt.Sprintf("ctx %d%%", int(float64(m.stats.ctxUsed)/float64(max(ctxMax, 1))*100)))
	money := sGreen.Render(fmt.Sprintf("$%.2f", m.stats.cost))
	if m.stats.unpriced {
		money += sYellow.Render("+?") // some models' prices are unknown
	}
	if hr := m.cacheHitRate(); hr >= 0 {
		money += sDim.Render(fmt.Sprintf(" · cache %d%%", hr))
	}
	moneyFull := money
	if m.cfg.Balance > 0 {
		moneyFull += sDim.Render(fmt.Sprintf(" · ~$%.2f left", m.cfg.Balance-m.cfg.Spent))
	}
	if m.stats.orCreditsOK && m.cfg.Provider == "openrouter" {
		moneyFull += sDim.Render(fmt.Sprintf(" · OR $%.2f", m.stats.orCredits))
	}
	folder := sSky.Render(m.folderLabel())
	prov := providerBadge(m.cfg.Provider)
	if plan := planStatus(m.cfg.Provider, false); plan != "" {
		// subscriptions don't cost per request: say what is used instead of a dollar amount
		money = plan
		moneyFull = planStatus(m.cfg.Provider, true)
		if m.stats.cost > 0 {
			moneyFull += sDim.Render(fmt.Sprintf(" · $%.2f on other models", m.stats.cost))
		}
	}

	// most detailed layout first; fall back to more compact ones as the window narrows
	layouts := [][]string{
		{model, prov, ctxFull, moneyFull, folder},
		{model, prov, ctxFull, moneyFull},
		{model, prov, ctxShort, moneyFull},
		{model, prov, ctxShort, money},
		{model, ctxShort, money},
		{model},
	}
	left := model
	for _, l := range layouts {
		cand := strings.Join(l, sep)
		if lipgloss.Width(cand) <= m.w {
			left = cand
			break
		}
	}

	var modeLine string
	switch m.cfg.Mode {
	case config.ModeAcceptEdits:
		modeLine = sViolet.Render("⏵⏵ accept edits on") + sDim.Render(" (shift+tab to cycle)")
	case config.ModePlan:
		modeLine = sSky.Render("⏸ plan mode on") + sDim.Render(" (read-only · shift+tab to cycle)")
	case config.ModeAuto:
		modeLine = sOrange.Render("⏵⏵⏵ auto mode on") + sDim.Render(" (no permission prompts · shift+tab to cycle)")
	default:
		modeLine = sDim.Render("? /help · shift+tab mode · ctrl+j newline · pgup/pgdn scroll")
	}
	if m.updated != "" {
		modeLine = sGreen.Render("↑ werkz "+m.updated+" installed · restart to use it") + sDim.Render(" · ") + modeLine
	}
	if m.flash != "" {
		modeLine = sYellow.Render(m.flash)
	}
	return left + "\n" + modeLine
}
