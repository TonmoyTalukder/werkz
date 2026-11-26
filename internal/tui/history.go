package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/agent"
	"github.com/TonmoyTalukder/werkz/internal/session"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// ---------------------------------------------------------------- transcript items

// pushItem adds a conversation item (saved with the conversation) to the transcript.
func (m *model) pushItem(it session.Item) tea.Cmd {
	item := it
	m.entries = append(m.entries, &entry{render: func(w int) string { return m.renderItem(item, w) }, w: -1, item: &item})
	m.follow = true
	return nil
}

func (m *model) renderItem(it session.Item, w int) string {
	switch it.Kind {
	case "user":
		return renderUser(it.Text, w)
	case "assistant":
		return m.renderAssistant(it.Text, w)
	case "tool":
		call := it.Call
		if it.Args != "" {
			call += sDim.Render("(" + it.Args + ")")
		}
		return renderToolResult(agent.ToolResultMsg{Name: it.Call, Summary: it.Text, Detail: it.Detail, IsError: it.IsError}, call, w)
	case "attach":
		return renderAttach(it, w)
	case "note":
		return wrapTo(sSky.Render("✻ ")+sText.Render(it.Text), w)
	}
	return wrapTo(it.Text, w)
}

// renderAttach shows what an @mention attached, under the message.
func renderAttach(it session.Item, w int) string {
	var parts []string
	for i, l := range strings.Split(it.Text, "\n") {
		if i < len(it.OK) && it.OK[i] {
			parts = append(parts, sMention.Render(l))
		} else {
			parts = append(parts, sRed.Render(l))
		}
	}
	return wrapTo(sDim.Render("  ⎿  attached ")+strings.Join(parts, sDim.Render(" · ")), w)
}

func attachItem(msg agent.AttachedMsg) session.Item {
	it := session.Item{Kind: "attach"}
	var lines []string
	for _, a := range msg.Items {
		lines = append(lines, a.Label)
		it.OK = append(it.OK, a.OK)
	}
	it.Text = strings.Join(lines, "\n")
	return it
}

// ---------------------------------------------------------------- saving

type sessionSavedMsg struct{ err error }

// newSession starts a new saved conversation (at start-up and after /clear).
func (m *model) newSession() {
	m.sess.id, m.sess.created = session.NewID(), time.Now()
}

// saveSession saves the conversation in the background.
func (m *model) saveSession() tea.Cmd {
	if m.cfg.NoHistory || m.eng == nil || m.sess.id == "" {
		return nil
	}
	var items []session.Item
	for _, e := range m.entries {
		if e.item != nil {
			items = append(items, *e.item)
		}
	}
	name, _, _ := m.eng.Workspace()
	m.sess.seq++
	s := &session.Session{Meta: session.Meta{ID: m.sess.id, Created: m.sess.created, Updated: time.Now(),
		Dirs: m.eng.Dirs(), Workspace: name, Provider: m.cfg.Provider, Model: m.cfg.Model},
		Items: items, History: session.FromHistory(m.eng.History())}
	seq := m.sess.seq
	return func() tea.Msg { return sessionSavedMsg{session.Save(s, seq)} }
}

func (m *model) handleSessionSaved(msg sessionSavedMsg) tea.Cmd {
	if msg.err == nil || m.sess.warned {
		return nil
	}
	m.sess.warned = true
	return m.printS(renderNotice("warn", "Couldn't save this conversation for /resume: "+msg.err.Error()))
}

// ---------------------------------------------------------------- resuming

// resumePicker is the /resume list: saved conversations, searchable.
type resumePicker struct {
	all      []session.Meta
	filter   textinput.Model
	sel      int
	anywhere bool   // all folders, not just this one
	del      string // conversation waiting for a second ctrl+x to be deleted
}

type resumeRow struct {
	meta    session.Meta
	excerpt string
}

func (m *model) openResume(query string) tea.Cmd {
	if m.turn.active {
		return m.printS(renderNotice("warn", "Wait for the current turn to finish (esc to interrupt)."))
	}
	all, err := session.List()
	if err != nil {
		return m.printS(renderNotice("error", "Couldn't read saved conversations: "+err.Error()))
	}
	others := all[:0]
	for _, s := range all {
		if s.ID != m.sess.id { // the conversation open now isn't offered
			others = append(others, s)
		}
	}
	all = others
	ti := textinput.New()
	ti.Placeholder = "search requests and answers"
	ti.Prompt = "⌕ "
	ti.PromptStyle = sPink
	ti.SetValue(query)
	ti.CursorEnd()
	m.rp = resumePicker{all: all, filter: ti}
	if len(m.resumeRows()) == 0 && len(all) > 0 {
		m.rp.anywhere = true // nothing from this folder yet: show the others
	}
	m.state = stateResume
	return m.rp.filter.Focus()
}

func (m *model) resumeRows() []resumeRow {
	var out []resumeRow
	here := ""
	if m.eng != nil {
		here = m.eng.Dirs()[0]
	}
	for _, s := range m.rp.all {
		if !m.rp.anywhere && !s.InFolder(here) {
			continue
		}
		if ok, ex := s.Match(m.rp.filter.Value()); ok {
			out = append(out, resumeRow{s, ex})
		}
	}
	return out
}

func (m *model) resumeKey(k tea.KeyMsg) tea.Cmd {
	rows := m.resumeRows()
	ks := k.String()
	if ks != "ctrl+x" {
		m.rp.del = ""
	}
	switch ks {
	case "esc":
		m.state = stateChat
		return nil
	case "up", "ctrl+p":
		if len(rows) > 0 {
			m.rp.sel = (m.rp.sel - 1 + len(rows)) % len(rows)
		}
		return nil
	case "down", "ctrl+n":
		if len(rows) > 0 {
			m.rp.sel = (m.rp.sel + 1) % len(rows)
		}
		return nil
	case "tab":
		m.rp.anywhere = !m.rp.anywhere
		m.rp.sel = 0
		return nil
	case "ctrl+x":
		if len(rows) == 0 {
			return nil
		}
		id := rows[m.rp.sel%len(rows)].meta.ID
		if m.rp.del != id {
			m.rp.del = id // ask: press again to delete
			return nil
		}
		m.rp.del = ""
		if err := session.Delete(id); err != nil {
			m.flash = "Couldn't delete: " + err.Error()
			return nil
		}
		for i, s := range m.rp.all {
			if s.ID == id {
				m.rp.all = append(m.rp.all[:i], m.rp.all[i+1:]...)
				break
			}
		}
		m.rp.sel = max(min(m.rp.sel, len(rows)-2), 0)
		return nil
	case "enter":
		if len(rows) == 0 {
			return nil
		}
		m.state = stateChat
		return m.resume(rows[m.rp.sel%len(rows)].meta.ID)
	}
	var cmd tea.Cmd
	before := m.rp.filter.Value()
	m.rp.filter, cmd = m.rp.filter.Update(k)
	if m.rp.filter.Value() != before {
		m.rp.sel = 0
	}
	return cmd
}

const resumeVisible = 7

func (m *model) resumeView() string {
	var b strings.Builder
	rows := m.resumeRows()
	scope := "this folder"
	if m.rp.anywhere {
		scope = "all folders"
	}
	b.WriteString(gradient("Resume a conversation", 0) + sDim.Render(fmt.Sprintf("  ·  %d in %s", len(rows), scope)) + "\n")
	b.WriteString(m.rp.filter.View() + "\n")
	if len(rows) == 0 {
		msg := "No other saved conversations yet: they are saved as you chat."
		switch {
		case m.rp.filter.Value() != "":
			msg = "Nothing matches. tab searches " + map[bool]string{true: "this folder", false: "all folders"}[m.rp.anywhere] + "."
		case len(m.rp.all) > 0 && !m.rp.anywhere:
			msg = "None in this folder. tab shows all folders."
		}
		b.WriteString(sDim.Render("  "+msg) + "\n")
	}
	sel := 0
	if len(rows) > 0 {
		sel = m.rp.sel % len(rows)
	}
	first := 0
	if sel >= resumeVisible {
		first = sel - resumeVisible + 1
	}
	w := max(m.w-4, 20)
	for i := first; i < min(len(rows), first+resumeVisible); i++ {
		r := rows[i]
		info := fmt.Sprintf("%s · %d request%s", ago(r.meta.Updated), r.meta.Prompts, plural(r.meta.Prompts))
		if m.rp.anywhere && len(r.meta.Dirs) > 0 {
			info += " · ▸ " + tildify(r.meta.Dirs[0])
		}
		if r.meta.Workspace != "" {
			info += " · " + r.meta.Workspace
		}
		title := r.meta.Title
		if i == sel {
			b.WriteString(ansi.Truncate(sPink.Render("❯ ")+sPink.Bold(true).Render(title), w, "…") + "\n")
		} else {
			b.WriteString(ansi.Truncate("  "+sText.Render(title), w, "…") + "\n")
		}
		b.WriteString(ansi.Truncate("    "+sDim.Render(info), w, "…") + "\n")
		if r.excerpt != "" {
			b.WriteString(ansi.Truncate("    "+sFaint.Render(r.excerpt), w, "…") + "\n")
		}
	}
	hint := "enter resume · ↑↓ move · tab " + map[bool]string{true: "this folder", false: "all folders"}[m.rp.anywhere] + " · ctrl+x delete · esc close"
	if m.rp.del != "" {
		hint = sYellow.Render("press ctrl+x again to delete this conversation for good") + sDim.Render(" · any other key keeps it")
		b.WriteString("  " + hint + "\n")
	} else {
		b.WriteString(sFaint.Render("  "+hint) + "\n")
	}
	return box(strings.TrimRight(b.String(), "\n"), cViolet, m.w)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ago is a short "how long ago": "just now", "5m ago", "3h ago", "yesterday", "Mar 4".
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case t.Year() == time.Now().Year():
		return t.Format("Jan 2")
	}
	return t.Format("Jan 2 2006")
}

// resume continues a saved conversation: its folders, model, transcript and history.
func (m *model) resume(id string) tea.Cmd {
	if m.turn.active {
		return m.printS(renderNotice("warn", "Wait for the current turn to finish (esc to interrupt)."))
	}
	s, err := session.Load(id)
	if err != nil {
		return m.printS(renderNotice("error", "Couldn't open that conversation: "+err.Error()))
	}
	save := m.saveSession() // the conversation being left, as it is now
	m.entries = nil
	m.push(m.banner)
	for _, it := range s.Items {
		m.pushItem(it)
	}
	var cmds []tea.Cmd
	if len(s.Dirs) > 0 && strings.Join(s.Dirs, "\x00") != m.dirsKey() {
		if ws := m.cfg.Workspace(s.Workspace); s.Workspace != "" && ws != nil {
			cmds = append(cmds, m.activateWorkspace(ws))
		} else if anyDir(s.Dirs) {
			cmds = append(cmds, m.activateDirs(s.Dirs, s.Workspace, nil))
		}
	}
	m.eng.Restore(session.ToHistory(s.History)) // after the folders: it notes their instructions as sent
	m.sess.id, m.sess.created, m.sess.seq = s.ID, s.Created, 0
	m.stats.ctxUsed, m.stats.warmUntil = 0, time.Time{}
	m.ment.ix, m.ment.stale = nil, true
	note := fmt.Sprintf("Resumed “%s” · %d request%s · last active %s", trunc(s.Title, 60), s.Prompts, plural(s.Prompts), ago(s.Updated))
	if s.Provider != "" && s.Model != "" && (s.Provider != m.cfg.Provider || s.Model != m.cfg.Model) && m.cfg.Configured(s.Provider) {
		m.selectModel(s.Provider, s.Model)
		note += " · model " + m.curModelInfo().Label
	}
	cmds = append(cmds, m.printS(renderNotice("info", note+". Files are read again before they're changed.")))
	if !anyDir(s.Dirs) && len(s.Dirs) > 0 {
		cmds = append(cmds, m.printS(renderNotice("warn", "Its folder is gone ("+tildify(s.Dirs[0])+"); continuing in "+tildify(m.eng.Dirs()[0])+".")))
	}
	return tea.Batch(append([]tea.Cmd{save}, cmds...)...)
}

func anyDir(dirs []string) bool {
	for _, d := range dirs {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return true
		}
	}
	return false
}

func trunc(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// continueLatest resumes the latest conversation in the folder werkz started in (--continue).
func (m *model) continueLatest() tea.Cmd {
	dir := m.startDir
	if m.eng != nil {
		dir = m.eng.Dirs()[0]
	}
	meta, err := session.Latest(dir)
	if err != nil {
		return m.printS(renderNotice("info", "No saved conversation in "+tildify(filepath.Clean(dir))+" yet · starting a new one (/resume lists the others)."))
	}
	return m.resume(meta.ID)
}
