package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// ---------------------------------------------------------------- mouse selection

// Drag over the conversation to select text; releasing the button copies it. werkz keeps the
// mouse for this (and for scrolling), so it works the same in every terminal. Positions are
// kept in transcript lines, so scrolling while a selection is shown keeps it in place.

var sSelected = lipgloss.NewStyle().Background(lipgloss.Color("#2B4C9B")).Foreground(lipgloss.Color("#FFFFFF"))

type textSel struct {
	on       bool // a selection is shown
	dragging bool
	aLine    int // anchor (where the drag started): transcript line and cell column
	aCol     int
	bLine    int // the other end (where the pointer is)
	bCol     int
}

// ordered returns the selection's start and end, start first; end column is exclusive.
func (s textSel) ordered() (l0, c0, l1, c1 int) {
	l0, c0, l1, c1 = s.aLine, s.aCol, s.bLine, s.bCol
	if l1 < l0 || (l1 == l0 && c1 < c0) {
		l0, c0, l1, c1 = l1, c1, l0, c0
	}
	return l0, c0, l1, c1 + 1
}

func (s textSel) empty() bool { return s.aLine == s.bLine && s.aCol == s.bCol }

// handleMouse scrolls with the wheel and selects with the left button.
func (m *model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	switch {
	case msg.Button == tea.MouseButtonWheelUp:
		m.vp.ScrollUp(3)
		m.follow = false
		return nil
	case msg.Button == tea.MouseButtonWheelDown:
		m.vp.ScrollDown(3)
		m.follow = m.vp.AtBottom()
		return nil
	}
	if m.state == stateLogin {
		return nil
	}
	line := m.vp.YOffset + min(max(msg.Y, 0), max(m.vp.Height-1, 0))
	col := max(msg.X, 0)
	switch msg.Action {
	case tea.MouseActionPress:
		if msg.Button != tea.MouseButtonLeft {
			return nil
		}
		m.sel = textSel{}
		if msg.Y >= m.vp.Height {
			return nil // a click below the conversation (input, status bar)
		}
		m.sel = textSel{dragging: true, aLine: line, aCol: col, bLine: line, bCol: col}
		m.follow = false // hold the view still while selecting
	case tea.MouseActionMotion:
		if !m.sel.dragging {
			return nil
		}
		// dragging past the top or bottom edge scrolls
		if msg.Y < 0 || (msg.Y == 0 && m.vp.YOffset > 0) {
			m.vp.ScrollUp(1)
		} else if msg.Y >= m.vp.Height-1 {
			m.vp.ScrollDown(1)
		}
		line = m.vp.YOffset + min(max(msg.Y, 0), max(m.vp.Height-1, 0))
		m.sel.bLine, m.sel.bCol = line, col
		m.sel.on = !m.sel.empty()
	case tea.MouseActionRelease:
		if !m.sel.dragging {
			return nil
		}
		m.sel.dragging = false
		if m.sel.empty() {
			m.sel = textSel{} // a plain click
			m.follow = m.vp.AtBottom()
			return nil
		}
		m.sel.on = true
		if text := m.selectedText(); text != "" {
			return copyText("the selection", text)
		}
	}
	return nil
}

// selectedText is the selected part of the transcript as plain text, without the padding
// at line ends.
func (m *model) selectedText() string {
	if !m.sel.on || len(m.lines) == 0 {
		return ""
	}
	l0, c0, l1, c1 := m.sel.ordered()
	var out []string
	for l := max(l0, 0); l <= min(l1, len(m.lines)-1); l++ {
		from, to := 0, m.w
		if l == l0 {
			from = c0
		}
		if l == l1 {
			to = c1
		}
		out = append(out, strings.TrimRight(ansi.Strip(ansi.Cut(m.lines[l], from, to)), " "))
	}
	return strings.Trim(strings.Join(out, "\n"), "\n")
}

// paintSelection highlights the selection in the visible part of the transcript.
func (m *model) paintSelection(view string) string {
	if !m.sel.on {
		return view
	}
	l0, c0, l1, c1 := m.sel.ordered()
	rows := strings.Split(view, "\n")
	for i, r := range rows {
		l := m.vp.YOffset + i
		if l < l0 || l > l1 {
			continue
		}
		from, to := 0, ansi.StringWidth(r)
		if l == l0 {
			from = c0
		}
		if l == l1 {
			to = min(c1, to)
		}
		if l != l1 {
			to = max(to, from+1) // show an empty line as selected too
		}
		if from >= to {
			continue
		}
		seg := ansi.Strip(ansi.Cut(r, from, to))
		if pad := to - from - ansi.StringWidth(seg); pad > 0 {
			seg += strings.Repeat(" ", pad)
		}
		rows[i] = ansi.Cut(r, 0, from) + sSelected.Render(seg) + ansi.Cut(r, to, ansi.StringWidth(r))
	}
	return strings.Join(rows, "\n")
}
