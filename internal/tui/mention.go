package tui

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/mention"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// ---------------------------------------------------------------- @ file picker

// Mentions look like inline code in the docs: a tinted chip.
var (
	sMention     = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFB86B")).Background(lipgloss.Color("#3A2A1A"))
	sMentionUser = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFB86B")).Background(lipgloss.Color("#45321E"))
)

const mentionRows = 8

type mentionState struct {
	ix       *mention.Index
	building bool
	stale    bool // files may have changed (a turn ran): rebuild when the picker opens
	sel      int  // highlighted row
	closed   int  // byte offset of the token the user closed with esc (-1 = none)
	used     map[string]int
	dirsKey  string // folders the index was built for
	results  []mention.Entry
	resultQ  string
	resultOK bool
}

type indexBuiltMsg struct {
	ix  *mention.Index
	key string
}

// mentionAt finds the @mention being typed at the cursor: the query after "@" (up to the
// cursor) and the byte range of the whole token in the value.
func (m *model) mentionAt() (q string, start, end int, ok bool) {
	v := m.input.Value()
	lines := strings.Split(v, "\n")
	row := min(m.input.Line(), len(lines)-1)
	li := m.input.LineInfo()
	col := li.StartColumn + li.ColumnOffset
	off := 0
	for i := 0; i < row; i++ {
		off += len(lines[i]) + 1
	}
	rs := []rune(lines[row])
	col = min(col, len(rs))
	cur := off + len(string(rs[:col]))
	// back to the start of the word
	s := cur
	for s > off && !isBlank(v[s-1]) {
		s--
	}
	if s >= len(v) || v[s] != '@' {
		return "", 0, 0, false
	}
	e := cur
	for e < len(v) && !isBlank(v[e]) {
		e++
	}
	q = strings.TrimPrefix(v[s+1:cur], `"`)
	if strings.Contains(q, `"`) {
		return "", 0, 0, false // a closed quote: the mention is complete
	}
	return q, s, e, true
}

func isBlank(b byte) bool { return b == ' ' || b == '\t' || b == '\n' }

func (m *model) dirsKey() string {
	if m.eng == nil {
		return ""
	}
	return strings.Join(m.eng.Dirs(), "\x00")
}

// buildIndex lists the working folders' files in the background.
func (m *model) buildIndex() tea.Cmd {
	if m.ment.building || m.eng == nil {
		return nil
	}
	m.ment.building = true
	dirs, key := m.eng.Dirs(), m.dirsKey()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return indexBuiltMsg{ix: mention.Build(ctx, dirs), key: key}
	}
}

func (m *model) handleIndexBuilt(msg indexBuiltMsg) tea.Cmd {
	m.ment.building = false
	m.ment.ix, m.ment.dirsKey, m.ment.stale = msg.ix, msg.key, false
	m.ment.resultOK = false
	if msg.key != m.dirsKey() {
		return m.buildIndex() // the folders changed while indexing
	}
	return nil
}

// mentionSugs returns the picker rows for the mention at the cursor (nil = no picker), and
// starts indexing when the index is missing or out of date.
func (m *model) mentionSugs() ([]mention.Entry, tea.Cmd) {
	if m.state != stateChat {
		return nil, nil
	}
	q, start, _, ok := m.mentionAt()
	if !ok || start == m.ment.closed {
		return nil, nil
	}
	var cmd tea.Cmd
	ix := m.ment.ix
	if ix == nil || m.ment.stale || m.ment.dirsKey != m.dirsKey() || time.Since(ix.Built) > 2*time.Minute {
		cmd = m.buildIndex()
	}
	if ix == nil {
		return []mention.Entry{}, cmd // the picker shows "indexing…"
	}
	if !m.ment.resultOK || m.ment.resultQ != q {
		m.ment.results = ix.Search(q, 50, m.ment.used)
		m.ment.resultQ, m.ment.resultOK = q, true
	}
	return m.ment.results, cmd
}

// acceptMention replaces the token at the cursor with the chosen entry. With drill, a folder
// is opened (its contents listed) instead of chosen.
func (m *model) acceptMention(e mention.Entry, drill bool) {
	_, start, end, ok := m.mentionAt()
	if !ok {
		return
	}
	v := m.input.Value()
	var ins string
	if e.Dir && drill {
		ins = "@" + e.Path + "/"
		if strings.ContainsAny(e.Path, " \t") {
			ins = `@"` + e.Path + "/"
		}
	} else {
		p := e.Path
		if e.Dir {
			p += "/"
		}
		ins = mention.Quote(p) + " "
		if m.ment.used == nil {
			m.ment.used = map[string]int{}
		}
		m.ment.used[e.Path] += 50 // mentioned before: rank it higher next time
	}
	rest := v[end:]
	if !(e.Dir && drill) && strings.HasPrefix(rest, " ") {
		rest = rest[1:]
	}
	m.setInputKeepCursor(v[:start]+ins, rest)
	m.ment.sel = 0
	m.ment.resultOK = false
	m.input.SetHeight(min(max(m.input.LineCount(), 1), 8))
}

// setInputKeepCursor sets the input to before+after with the cursor between the two.
func (m *model) setInputKeepCursor(before, after string) {
	m.input.SetValue(before)
	if after == "" {
		return
	}
	if !strings.Contains(after, "\n") {
		col := len([]rune(before[strings.LastIndexByte(before, '\n')+1:]))
		m.input.InsertString(after)
		m.input.SetCursor(col)
		return
	}
	m.input.InsertString(after)
	for range []rune(after) {
		m.input, _ = m.input.Update(tea.KeyMsg{Type: tea.KeyLeft})
	}
}

// mentionKey handles keys while the picker is open; handled is false for keys it leaves to
// the input (typing, moving the cursor).
func (m *model) mentionKey(k tea.KeyMsg, rows []mention.Entry) (handled bool) {
	switch k.String() {
	case "up", "ctrl+p":
		if len(rows) > 0 {
			m.ment.sel = (m.ment.sel - 1 + len(rows)) % len(rows)
		}
		return true
	case "down", "ctrl+n":
		if len(rows) > 0 {
			m.ment.sel = (m.ment.sel + 1) % len(rows)
		}
		return true
	case "tab":
		if len(rows) > 0 {
			m.acceptMention(rows[m.ment.sel%len(rows)], true)
		}
		return true
	case "enter":
		if len(rows) == 0 {
			return false // nothing to choose: Enter sends
		}
		m.acceptMention(rows[m.ment.sel%len(rows)], false)
		return true // never sends in the same keystroke
	case "esc":
		_, start, _, _ := m.mentionAt()
		m.ment.closed = start
		return true
	}
	return false
}

func (m *model) mentionView(rows []mention.Entry) string {
	var b strings.Builder
	if len(rows) == 0 {
		if m.ment.ix == nil {
			b.WriteString(sDim.Render("  indexing files…") + "\n")
		} else {
			b.WriteString(sDim.Render("  no matching files") + "\n")
		}
		return b.String()
	}
	sel := m.ment.sel % len(rows)
	first := 0
	if sel >= mentionRows {
		first = sel - mentionRows + 1
	}
	for i := first; i < min(len(rows), first+mentionRows); i++ {
		e := rows[i]
		dir, base := path.Split(e.Path)
		name := base
		if e.Dir {
			name += "/"
		}
		w := max(m.w-6, 20)
		if i == sel {
			line := sPink.Render("❯ ") + sDim.Render(dir) + sPink.Bold(true).Render(name)
			b.WriteString(ansi.Truncate(line, w, "…") + "\n")
		} else {
			line := "  " + sFaint.Render(dir) + sText.Render(name)
			if e.Dir {
				line = "  " + sFaint.Render(dir) + sSky.Render(name)
			}
			b.WriteString(ansi.Truncate(line, w, "…") + "\n")
		}
	}
	hint := "↑↓ move · enter choose · tab open folder · esc close"
	if n := len(rows); n > mentionRows {
		count := fmt.Sprint(n)
		if n >= 50 {
			count = "50+"
		}
		hint += " · " + count + " matches"
	}
	b.WriteString(sFaint.Render("  "+hint) + "\n")
	return b.String()
}

// ---------------------------------------------------------------- highlighting

// highlightInput colours the @mentions in the input box's rendered view (the textarea can't
// style ranges itself). A mention with the cursor inside it is left as it is.
func highlightInput(view string) string {
	lines := strings.Split(view, "\n")
	for i, l := range lines {
		plain := ansi.Strip(l)
		toks := mention.Find(plain)
		if len(toks) == 0 {
			continue
		}
		var b strings.Builder
		pos := 0 // cell column reached in l
		for _, t := range toks {
			s := ansi.StringWidth(plain[:t.Start])
			e := ansi.StringWidth(plain[:t.End])
			seg := ansi.Cut(l, s, e)
			if strings.Contains(seg, "\x1b") {
				continue // the cursor is in it
			}
			b.WriteString(ansi.Cut(l, pos, s))
			b.WriteString(sMention.Render(seg))
			pos = e
		}
		b.WriteString(ansi.Cut(l, pos, ansi.StringWidth(l)))
		lines[i] = b.String()
	}
	return strings.Join(lines, "\n")
}

// userLine renders one line of a sent message: the message background, with mentions as
// chips that keep it (a plain reset would end the background mid-line).
func userLine(l string, width int) string {
	toks := mention.Find(l)
	var b strings.Builder
	pos := 0
	for _, t := range toks {
		b.WriteString(sUserMsg.Render(l[pos:t.Start]))
		b.WriteString(sMentionUser.Render(l[t.Start:t.End]))
		pos = t.End
	}
	b.WriteString(sUserMsg.Render(l[pos:]))
	if pad := width - ansi.StringWidth(l); pad > 0 {
		b.WriteString(sUserMsg.Render(strings.Repeat(" ", pad)))
	}
	return b.String()
}
