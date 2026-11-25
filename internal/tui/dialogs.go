package tui

import (
	"fmt"
	"strings"

	"github.com/TonmoyTalukder/werkz/internal/agent"
	"github.com/TonmoyTalukder/werkz/internal/catalog"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------------- overlays: model picker

type picker struct {
	all     []catalog.ModelInfo
	shown   []catalog.ModelInfo
	sel     int
	offset  int
	filter  textinput.Model
	curProv string
	curID   string
}

func newPicker(all []catalog.ModelInfo, prov, id string) picker {
	ti := textinput.New()
	ti.Placeholder = "type to filter…"
	ti.Prompt = "⌕ "
	ti.PromptStyle = sViolet
	p := picker{all: all, filter: ti, curProv: prov, curID: id}
	p.apply()
	for i, mi := range p.shown {
		if mi.Provider == prov && mi.ID == id {
			p.sel = i
		}
	}
	return p
}

func (p *picker) refresh(all []catalog.ModelInfo) {
	p.all = all
	p.apply()
}

func (p *picker) apply() {
	q := strings.ToLower(p.filter.Value())
	p.shown = p.shown[:0]
	for _, mi := range p.all {
		if q == "" || strings.Contains(strings.ToLower(mi.ID+" "+mi.Label+" "+mi.Provider), q) {
			p.shown = append(p.shown, mi)
		}
	}
	if p.sel >= len(p.shown) {
		p.sel = max(len(p.shown)-1, 0)
	}
}

func (m *model) pickerKey(k tea.KeyMsg) tea.Cmd {
	p := &m.picker
	switch k.String() {
	case "esc":
		m.state = stateChat
		return nil
	case "up", "ctrl+p":
		if p.sel > 0 {
			p.sel--
		}
		return nil
	case "down", "ctrl+n":
		if p.sel < len(p.shown)-1 {
			p.sel++
		}
		return nil
	case "enter":
		if len(p.shown) == 0 {
			return nil
		}
		mi := p.shown[p.sel]
		if mi.ID != m.cfg.Model {
			if w := m.cacheWarn("Switching model"); w != "" {
				m.printS(renderNotice("warn", w))
			}
		}
		m.selectModel(mi.Provider, mi.ID)
		m.saveConfig()
		m.state = stateChat
		return m.printS(renderNotice("info", fmt.Sprintf("Model set to %s via %s · effort %s", mi.Label, mi.Provider, m.cfg.Effort)))
	}
	var cmd tea.Cmd
	p.filter, cmd = p.filter.Update(k)
	p.apply()
	return cmd
}

func (m *model) pickerView() string {
	p := &m.picker
	rows := min(14, max(m.h-12, 5))
	if p.sel < p.offset {
		p.offset = p.sel
	}
	if p.sel >= p.offset+rows {
		p.offset = p.sel - rows + 1
	}
	var b strings.Builder
	b.WriteString(gradient("Select model", 0) + sDim.Render("  ·  enter select · esc cancel") + "\n")
	b.WriteString(p.filter.View() + "\n\n")
	if len(p.shown) == 0 {
		b.WriteString(sDim.Render("  no models match"))
	}
	for i := p.offset; i < min(len(p.shown), p.offset+rows); i++ {
		mi := p.shown[i]
		cursor := "  "
		name := sText.Render(padRight(mi.Label, 34))
		if i == p.sel {
			cursor = sPink.Render("❯ ")
			name = sPink.Bold(true).Render(padRight(mi.Label, 34))
		}
		cur := " "
		if mi.Provider == p.curProv && mi.ID == p.curID {
			cur = sGreen.Render("●")
		}
		price := mi.PriceTag()
		if mi.FreeKind() != "" {
			price = sGreen.Render(price)
		} else {
			price = sDim.Render(price)
		}
		line := fmt.Sprintf("%s%s %s %s %s %s %s", cursor, cur, name,
			padRight(providerBadge(mi.Provider), 12), sDim.Render(padRight(catalog.HumanTokens(mi.Context)+" ctx", 10)),
			padRight(price, 12), sFaint.Render(mi.Note))
		b.WriteString(line + "\n")
	}
	if len(p.shown) > rows {
		b.WriteString(sDim.Render(fmt.Sprintf("  %d/%d", p.sel+1, len(p.shown))))
	}
	if m.cfg.Configured("openrouter") && !m.cfg.OpenRouterPaid {
		b.WriteString("\n" + sFaint.Render("  paid OpenRouter models are hidden · /openrouter paid to show them"))
	}
	return box(b.String(), cViolet, m.w)
}

// ---------------------------------------------------------------- overlays: effort

func (m *model) effortKey(k tea.KeyMsg) tea.Cmd {
	mi := m.curModelInfo()
	switch k.String() {
	case "esc":
		m.state = stateChat
	case "left", "h":
		if m.effortIdx > 0 {
			m.effortIdx--
		}
	case "right", "l":
		if m.effortIdx < len(mi.Efforts)-1 {
			m.effortIdx++
		}
	case "f", "tab":
		if mi.FastIn > 0 {
			m.cfg.Fast = !m.cfg.Fast
		}
	case "enter":
		if m.cfg.Effort != mi.Efforts[m.effortIdx] {
			if w := m.cacheWarn("Changing effort"); w != "" {
				m.printS(renderNotice("warn", w))
			}
		}
		m.cfg.Effort = mi.Efforts[m.effortIdx]
		m.saveConfig()
		m.state = stateChat
		fast := ""
		if m.cfg.Fast {
			fast = " · ⚡ fast"
		}
		return m.printS(renderNotice("info", "Effort set to "+m.cfg.Effort+fast))
	}
	return nil
}

func (m *model) effortView() string {
	mi := m.curModelInfo()
	var parts []string
	for i, e := range mi.Efforts {
		if i == m.effortIdx {
			parts = append(parts, lipgloss.NewStyle().Background(lipgloss.Color(blend(brandStops, float64(i)/float64(max(len(mi.Efforts)-1, 1))))).
				Foreground(lipgloss.Color("#0B0B0F")).Bold(true).Padding(0, 1).Render(e))
		} else {
			parts = append(parts, sDim.Padding(0, 1).Render(e))
		}
	}
	bar := strings.Join(parts, sFaint.Render("─"))
	desc := map[string]string{
		"auto": "let the provider decide", "low": "quick answers, least thinking & cost", "medium": "balanced",
		"high": "deeper reasoning", "xhigh": "best for hard coding & agentic work", "max": "maximum thinking, highest cost",
	}[mi.Efforts[m.effortIdx]]
	fast := ""
	if mi.FastIn > 0 {
		state := sDim.Render("off")
		if m.cfg.Fast {
			state = sYellow.Bold(true).Render("⚡ on")
		}
		fast = "\n" + sDim.Render("fast mode: ") + state + sDim.Render(fmt.Sprintf("  (f to toggle · $%g/$%g per MTok)", mi.FastIn, mi.FastOut))
	}
	body := gradient("Thinking effort", 0) + sDim.Render("  ·  "+mi.Label+"  ·  ←/→ adjust · enter save · esc cancel") +
		"\n\n" + bar + "\n" + sItalic.Render(desc) + fast
	return box(body, cSky, m.w)
}

// ---------------------------------------------------------------- overlays: permission

// permOptions are the answers offered; "don't ask again" only when the engine offers it.
func (m *model) permOptions() ([]string, []agent.PermDecision) {
	opts := []string{"Yes"}
	dec := []agent.PermDecision{agent.PermYes}
	if m.perm.Always != "" {
		opts = append(opts, m.perm.Always)
		dec = append(dec, agent.PermAlways)
	}
	opts = append(opts, "No, and tell werkz what to do differently (esc)")
	dec = append(dec, agent.PermNo)
	return opts, dec
}

func (m *model) permKey(k tea.KeyMsg) {
	if m.perm == nil {
		return
	}
	_, dec := m.permOptions()
	n := len(dec)
	choose := -1
	// Arrows + Enter only: letters and digits typed into a half-written message must never
	// answer the dialog.
	switch k.String() {
	case "up":
		m.permSel = (m.permSel + n - 1) % n
	case "down", "tab":
		m.permSel = (m.permSel + 1) % n
	case "esc":
		choose = n - 1
	case "enter":
		choose = m.permSel
	}
	if choose >= 0 {
		m.perm.Reply <- dec[choose]
		m.perm = nil
		m.state = stateChat
	}
}

func (m *model) permView() string {
	p := m.perm
	var b strings.Builder
	b.WriteString(sYellow.Bold(true).Render(p.Tool) + " " + sText.Render(p.Summary) + "\n")
	if len(p.Detail) > 0 {
		b.WriteString("\n" + renderDetail(p.Detail, m.w-8) + "\n")
	}
	b.WriteString("\n" + sText.Render("Do you want to proceed?") + sDim.Render("  ↑/↓ choose · enter confirm · esc no") + "\n")
	opts, _ := m.permOptions()
	for i, o := range opts {
		if i == m.permSel {
			b.WriteString(sPink.Render("❯ ") + sPink.Bold(true).Render(o) + "\n")
		} else {
			b.WriteString(sDim.Render("  "+o) + "\n")
		}
	}
	return box(strings.TrimRight(b.String(), "\n"), cYellow, m.w)
}
