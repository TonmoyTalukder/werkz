package tui

import (
	"strings"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/session"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/x/ansi"
)

type entry struct {
	render func(w int) string
	w      int
	out    string
	item   *session.Item // a request, answer, tool use... (saved with the conversation); nil for notices
}

// push adds a transcript entry rendered lazily at the current width.
func (m *model) push(f func(w int) string) tea.Cmd {
	m.entries = append(m.entries, &entry{render: f, w: -1})
	m.follow = true
	return nil
}

// printS adds a pre-styled line/block that is word-wrapped to the width.
func (m *model) printS(s string) tea.Cmd {
	return m.push(func(w int) string { return wrapTo(s, w) })
}

func wrapTo(s string, w int) string {
	w = max(w, 10)
	return ansi.Hardwrap(ansi.Wordwrap(s, w, " -/"), w, true)
}

func (m *model) history(w int) string {
	parts := make([]string, 0, len(m.entries))
	for i, e := range m.entries {
		if e.w != w {
			e.out, e.w = e.render(w), w
		}
		if m.state == stateSelect && i == m.selEntry {
			parts = append(parts, markSelected(e.out))
			continue
		}
		parts = append(parts, e.out)
	}
	return strings.Join(parts, "\n")
}

func (m *model) mdFor(w int) *glamour.TermRenderer {
	if m.mdCache == nil {
		m.mdCache = map[int]*glamour.TermRenderer{}
	}
	if r, ok := m.mdCache[w]; ok {
		return r
	}
	r, err := glamour.NewTermRenderer(glamour.WithStandardStyle(mdStyle()), glamour.WithWordWrap(max(w-4, 20)))
	if err != nil {
		return nil
	}
	m.mdCache[w] = r
	return r
}

// flushLive prints the streamed thinking and text segment into the scrollback.
func (m *model) flushLive(suffix string) {
	if t := strings.TrimSpace(m.turn.thinking.String()); t != "" {
		d := time.Since(m.turn.thinkStart)
		m.push(func(w int) string { return renderThinking(t, d, w) })
	}
	if t := strings.TrimSpace(m.turn.live.String()); t != "" {
		m.pushItem(session.Item{Kind: "assistant", Text: t + suffix})
	}
	m.turn.thinking.Reset()
	m.turn.live.Reset()
}

// clipLines truncates every line to w cells (and to h lines when h > 0) so the terminal never wraps.
func clipLines(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	if h > 0 && len(lines) > h {
		lines = lines[len(lines)-h:]
	}
	for i, l := range lines {
		if ansi.StringWidth(l) > w {
			lines[i] = ansi.Truncate(l, max(w, 1), "")
		}
	}
	return strings.Join(lines, "\n")
}
