package tui

import (
	"fmt"
	"strings"

	"github.com/TonmoyTalukder/werkz/internal/agent"
	"github.com/TonmoyTalukder/werkz/internal/catalog"
	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/textutil"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// fbChain is the "which models should werkz fall back to?" chooser shown after picking
// Ask or Auto in /fallback (or with /fallback models). The order you tick them is the order tried.
type fbChain struct {
	mode    string // fallback mode being saved with the chain
	filter  textinput.Model
	all     []catalog.ModelInfo
	shown   []catalog.ModelInfo
	chosen  []catalog.ModelInfo
	sel     int
	offset  int
	current catalog.ModelInfo
}

func (m *model) fallbackModeLabel() string {
	return map[string]string{
		"ask":  sGreen.Render("ask") + sDim.Render(" · you approve every switch"),
		"auto": sYellow.Render("auto") + sDim.Render(" · switches without asking"),
		"off":  sRed.Render("off") + sDim.Render(" · never switches; failed requests stop"),
	}[m.cfg.Fallback()]
}

func (m *model) openFallbackChain(mode string) tea.Cmd {
	ti := textinput.New()
	ti.Placeholder = "type to filter…"
	ti.Prompt = "⌕ "
	ti.PromptStyle = sViolet
	c := fbChain{mode: mode, filter: ti, current: m.curModelInfo()}
	for _, f := range m.cfg.FallbackChain {
		c.chosen = append(c.chosen, catalog.Lookup(f.Provider, f.Model))
	}
	c.refresh(m.cfg)
	m.fbc = c
	m.state = stateFallbackChain
	cmds := []tea.Cmd{m.fbc.filter.Focus()}
	for _, p := range config.Providers {
		if p.ID != "anthropic" && m.cfg.Configured(p.ID) {
			cmds = append(cmds, fetchModels(m.cfg, p.ID))
		}
	}
	return tea.Batch(cmds...)
}

func (c *fbChain) refresh(cfg *config.Config) {
	c.all = c.all[:0]
	for _, mi := range catalog.All(cfg) {
		if mi.Provider == c.current.Provider && mi.ID == c.current.ID {
			continue // the current model is always tried first anyway
		}
		c.all = append(c.all, mi)
	}
	c.apply()
}

func (c *fbChain) apply() {
	q := strings.ToLower(c.filter.Value())
	c.shown = c.shown[:0]
	for _, mi := range c.all {
		if q == "" || strings.Contains(strings.ToLower(mi.ID+" "+mi.Label+" "+mi.Provider), q) {
			c.shown = append(c.shown, mi)
		}
	}
	if c.sel >= len(c.shown) {
		c.sel = max(len(c.shown)-1, 0)
	}
}

func (c *fbChain) index(mi catalog.ModelInfo) int {
	for i, x := range c.chosen {
		if x.Provider == mi.Provider && x.ID == mi.ID {
			return i
		}
	}
	return -1
}

func (c *fbChain) toggle(mi catalog.ModelInfo) {
	if i := c.index(mi); i >= 0 {
		c.chosen = append(c.chosen[:i], c.chosen[i+1:]...)
		return
	}
	c.chosen = append(c.chosen, mi)
}

func (m *model) fallbackChainKey(k tea.KeyMsg) tea.Cmd {
	c := &m.fbc
	switch k.String() {
	case "esc":
		m.state = stateFallbackMode // back to the mode choice
		return nil
	case "up", "ctrl+p":
		if c.sel > 0 {
			c.sel--
		}
		return nil
	case "down", "ctrl+n":
		if c.sel < len(c.shown)-1 {
			c.sel++
		}
		return nil
	case " ", "tab":
		if len(c.shown) > 0 {
			c.toggle(c.shown[c.sel])
		}
		return nil
	case "ctrl+x":
		c.chosen = nil
		return nil
	case "enter":
		m.cfg.FallbackMode = c.mode
		m.cfg.FallbackChain = nil
		for _, mi := range c.chosen {
			m.cfg.FallbackChain = append(m.cfg.FallbackChain, config.FallbackModel{Provider: mi.Provider, Model: mi.ID})
		}
		m.saveConfig()
		m.state = stateChat
		title := map[string]string{"ask": "Ask", "auto": "Auto"}[c.mode]
		if len(c.chosen) == 0 {
			return m.printS(renderNotice("info", "Fallback: "+title+" · using one default model per connected provider (/fallback models to choose)"))
		}
		var names []string
		for _, mi := range c.chosen {
			names = append(names, mi.Label)
		}
		return m.printS(renderNotice("info", "Fallback: "+title+" → "+strings.Join(names, " → ")+" (saved as your default)"))
	}
	var cmd tea.Cmd
	c.filter, cmd = c.filter.Update(k)
	c.sel = 0
	c.apply()
	return cmd
}

func (m *model) fallbackChainView() string {
	c := &m.fbc
	var b strings.Builder
	title := map[string]string{"ask": "Ask", "auto": "Auto"}[c.mode]
	b.WriteString(gradient("Fallback models ("+title+")", 0) +
		sDim.Render("  ·  space add/remove · enter save · ctrl+x clear · esc back") + "\n")

	// the chain as it will be tried
	chain := sSky.Render(c.current.Label) + sDim.Render(" (current)")
	for _, mi := range c.chosen {
		chain += sDim.Render(" → ") + sText.Render(mi.Label)
	}
	if len(c.chosen) == 0 {
		chain += sDim.Render(" → none chosen (uses one default model per provider)")
	}
	b.WriteString(wrapTo(sDim.Render("order: ")+chain, max(m.w-6, 20)) + "\n")
	b.WriteString(c.filter.View() + "\n")

	rows := min(12, max(m.h-16, 3))
	if c.sel < c.offset {
		c.offset = c.sel
	}
	if c.sel >= c.offset+rows {
		c.offset = c.sel - rows + 1
	}
	if len(c.shown) == 0 {
		b.WriteString(sDim.Render("  no models match · connect more providers with /login") + "\n")
	}
	for i := c.offset; i < min(len(c.shown), c.offset+rows); i++ {
		mi := c.shown[i]
		mark := sFaint.Render("[ ]")
		if n := c.index(mi); n >= 0 {
			mark = sPink.Bold(true).Render(fmt.Sprintf("[%d]", n+1))
		}
		cursor := "  "
		name := sText.Render(padRight(textutil.Truncate(mi.Label, 34), 34))
		if i == c.sel {
			cursor = sPink.Render("❯ ")
			name = sPink.Bold(true).Render(padRight(textutil.Truncate(mi.Label, 34), 34))
		}
		price, paid := catalog.PriceLabel(mi)
		tag := sGreen.Render("free")
		if paid {
			tag = sYellow.Render(textutil.Truncate(strings.TrimPrefix(price, "paid · "), 22))
		}
		b.WriteString(fmt.Sprintf("%s%s %s %s %s\n", cursor, mark, name, padRight(providerBadge(mi.Provider), 12), tag))
	}
	if c.mode == "auto" {
		b.WriteString(sYellow.Render("Auto switches without asking, so paid models in this list can be used automatically."))
	} else {
		b.WriteString(sDim.Render("Ask shows each switch (and whether it's free or paid) before it happens."))
	}
	return box(b.String(), cSky, m.w)
}

// ---------------------------------------------------------------- mid-prompt fallback dialog

// fbBrowseList is the searchable "Choose another model…" list inside the fallback dialog.
type fbBrowseList struct {
	filter textinput.Model
	all    []catalog.ModelInfo
	shown  []catalog.ModelInfo
	sel    int
	offset int
}

func excluded(ex []config.FallbackModel, mi catalog.ModelInfo) bool {
	for _, x := range ex {
		if x.Provider == mi.Provider && (x.Model == "" || x.Model == mi.ID) {
			return true
		}
	}
	return false
}

func (m *model) openFallbackBrowse() tea.Cmd {
	ti := textinput.New()
	ti.Placeholder = "type to filter…"
	ti.Prompt = "⌕ "
	ti.PromptStyle = sViolet
	l := fbBrowseList{filter: ti}
	for _, mi := range catalog.All(m.cfg) {
		if !excluded(m.fb.Exclude, mi) {
			l.all = append(l.all, mi)
		}
	}
	l.apply()
	m.fbList, m.fbBrowse = l, true
	return m.fbList.filter.Focus()
}

func (l *fbBrowseList) apply() {
	q := strings.ToLower(l.filter.Value())
	l.shown = l.shown[:0]
	for _, mi := range l.all {
		if q == "" || strings.Contains(strings.ToLower(mi.ID+" "+mi.Label+" "+mi.Provider), q) {
			l.shown = append(l.shown, mi)
		}
	}
	if l.sel >= len(l.shown) {
		l.sel = max(len(l.shown)-1, 0)
	}
}

func (m *model) answerFallback(ch agent.FallbackChoice) tea.Cmd {
	if m.fb != nil {
		m.fb.Reply <- ch
	}
	m.fb, m.fbBrowse, m.state = nil, false, stateChat
	return nil
}

func (m *model) fallbackKey(k tea.KeyMsg) tea.Cmd {
	f := m.fb
	if m.fbBrowse {
		l := &m.fbList
		switch k.String() {
		case "esc":
			m.fbBrowse = false
			return nil
		case "up", "ctrl+p":
			if l.sel > 0 {
				l.sel--
			}
			return nil
		case "down", "ctrl+n":
			if l.sel < len(l.shown)-1 {
				l.sel++
			}
			return nil
		case "enter", "ctrl+a":
			if len(l.shown) == 0 {
				return nil
			}
			mi := l.shown[l.sel]
			return m.answerFallback(agent.FallbackChoice{Provider: mi.Provider, Model: mi.ID, Session: k.String() == "ctrl+a"})
		}
		var cmd tea.Cmd
		l.filter, cmd = l.filter.Update(k)
		l.sel = 0
		l.apply()
		return cmd
	}
	n := len(f.Candidates) + 2 // candidates, "choose another", "stop"
	pick := func(i int, session bool) tea.Cmd {
		switch {
		case i < len(f.Candidates):
			c := f.Candidates[i]
			return m.answerFallback(agent.FallbackChoice{Provider: c.Provider, Model: c.Model, Session: session})
		case i == len(f.Candidates):
			return m.openFallbackBrowse()
		default:
			return m.answerFallback(agent.FallbackChoice{Stop: true})
		}
	}
	switch ks := k.String(); ks {
	case "up", "k":
		m.fbSel = (m.fbSel + n - 1) % n
	case "down", "j", "tab":
		m.fbSel = (m.fbSel + 1) % n
	case "esc", "n":
		return m.answerFallback(agent.FallbackChoice{Stop: true})
	case "enter":
		return pick(m.fbSel, false)
	case "a":
		if m.fbSel < len(f.Candidates) {
			return pick(m.fbSel, true)
		}
	case "o":
		return m.openFallbackBrowse()
	default:
		if len(ks) == 1 && ks[0] >= '1' && ks[0] <= '9' {
			if i := int(ks[0] - '1'); i < len(f.Candidates) {
				return pick(i, false)
			}
		}
	}
	return nil
}

func modelRow(mi catalog.ModelInfo, w int) string {
	price, paid := catalog.PriceLabel(mi)
	tag := sGreen.Render("free")
	if paid {
		tag = sYellow.Render(textutil.Truncate(strings.TrimPrefix(price, "paid · "), 30))
	}
	return padRight(textutil.Truncate(mi.Label, max(min(w-50, 36), 12)), max(min(w-50, 36), 12)) + " " + padRight(providerBadge(mi.Provider), 12) + " " + tag
}

func (m *model) fallbackView() string {
	f := m.fb
	var b strings.Builder
	b.WriteString(sYellow.Bold(true).Render("↯ "+f.From) + sDim.Render(" · "+catalog.Lookup(f.From, f.FromModel).Label+" failed") + "\n")
	b.WriteString(wrapTo(sYellow.Render(f.Reason), max(m.w-6, 20)) + "\n\n")

	if m.fbBrowse {
		l := &m.fbList
		b.WriteString(sText.Render("Switch to which model?") + sDim.Render("  enter this message · ctrl+a whole session · esc back") + "\n")
		b.WriteString(l.filter.View() + "\n")
		rows := min(10, max(m.h-16, 3))
		if l.sel < l.offset {
			l.offset = l.sel
		}
		if l.sel >= l.offset+rows {
			l.offset = l.sel - rows + 1
		}
		if len(l.shown) == 0 {
			b.WriteString(sDim.Render("  no other connected models · add keys with /login") + "\n")
		}
		for i := l.offset; i < min(len(l.shown), l.offset+rows); i++ {
			row := modelRow(l.shown[i], m.w)
			if i == l.sel {
				b.WriteString(sPink.Render("❯ ") + sPink.Bold(true).Render(row) + "\n")
			} else {
				b.WriteString("  " + row + "\n")
			}
		}
		return box(strings.TrimRight(b.String(), "\n"), cOrange, m.w)
	}

	b.WriteString(sText.Render("Switch to which model?") + sDim.Render("  enter this message · a whole session · esc stop") + "\n")
	opts := make([]string, 0, len(f.Candidates)+2)
	for i, c := range f.Candidates {
		opts = append(opts, fmt.Sprintf("%d. %s", i+1, modelRow(catalog.Lookup(c.Provider, c.Model), m.w)))
	}
	opts = append(opts, "Choose another model…", "No, stop here · pick a model later with /model")
	for i, o := range opts {
		if i == m.fbSel {
			b.WriteString(sPink.Render("❯ ") + sPink.Bold(true).Render(o) + "\n")
		} else {
			b.WriteString("  " + sText.Render(o) + "\n")
		}
	}
	if len(f.Candidates) == 0 {
		b.WriteString(sFaint.Render("No fallback models set (/fallback models); choose one from the list.") + "\n")
	}
	return box(strings.TrimRight(b.String(), "\n"), cOrange, m.w)
}

// ---------------------------------------------------------------- overlay: fallback mode picker

var fallbackModes = []struct{ ID, Title, Desc string }{
	{"off", "Off", "Never switch models. If the model fails, the request stops and you choose what to do."},
	{"ask", "Ask", "When the model fails, ask before switching. Shows the next model and whether it is free or paid."},
	{"auto", "Auto", "Switch to the next provider in the fallback order without asking (may use paid models)."},
}

func (m *model) fallbackModeKey(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		m.state = stateChat
	case "up", "k":
		m.fbModeSel = (m.fbModeSel + len(fallbackModes) - 1) % len(fallbackModes)
	case "down", "j", "tab":
		m.fbModeSel = (m.fbModeSel + 1) % len(fallbackModes)
	case "1", "2", "3":
		m.fbModeSel = int(k.String()[0] - '1')
		fallthrough
	case "enter":
		mode := fallbackModes[m.fbModeSel]
		if mode.ID != "off" {
			return m.openFallbackChain(mode.ID) // next: choose which models to fall back to
		}
		m.cfg.FallbackMode = mode.ID
		m.saveConfig()
		m.state = stateChat
		return m.printS(renderNotice("info", "Fallback: "+mode.Title+" (saved as your default)"))
	}
	return nil
}

func (m *model) fallbackModeView() string {
	var b strings.Builder
	b.WriteString(gradient("Fallback when a model fails", 0) + sDim.Render("  ·  ↑/↓ choose · enter save as default · esc cancel") + "\n\n")
	cur := m.cfg.Fallback()
	for i, md := range fallbackModes {
		title := fmt.Sprintf("%d. %s", i+1, md.Title)
		if md.ID == "off" {
			title += " (default)"
		}
		if md.ID == cur {
			title += "  ✓"
		}
		if i == m.fbModeSel {
			b.WriteString(sPink.Render("❯ ") + sPink.Bold(true).Render(title) + "\n")
		} else {
			b.WriteString("  " + sText.Render(title) + "\n")
		}
		b.WriteString(wrapTo(sDim.Render("   "+md.Desc), max(m.w-6, 20)) + "\n")
	}
	var order []string
	for _, id := range m.cfg.FallbackOrder {
		if id != m.cfg.Provider && m.cfg.Configured(id) {
			order = append(order, id)
		}
	}
	if len(order) > 0 {
		b.WriteString("\n" + sFaint.Render("order: "+strings.Join(order, " → ")+" · change with /fallback gemini,ollama"))
	} else {
		b.WriteString("\n" + sFaint.Render("no other providers connected · add keys with /login"))
	}
	return box(b.String(), cSky, m.w)
}
