package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TonmoyTalukder/werkz/internal/agent"
	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/session"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func chatModel(t *testing.T, dirs ...string) *model {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Load()
	cfg.Onboarded = true
	cfg.Keys["anthropic"] = "sk-ant-x"
	m := newModel(cfg)
	m.state = stateChat
	m.ensureProvider(true)
	m.eng = agent.New(cfg, func(any) {})
	if len(dirs) > 0 {
		m.eng.SetWorkspace(dirs, nil, "")
	}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m
}

func typeKeys(m *model, s string) {
	for _, r := range s {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

// run executes a command and feeds its message back, like the program loop (one level).
func run(m *model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		for _, c := range b {
			run(m, c)
		}
		return
	}
	if msg != nil {
		_, next := m.Update(msg)
		_ = next
	}
}

func TestMentionPicker(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"src/auth/login.ts", "src/app.ts", "README.md"} {
		_ = os.MkdirAll(filepath.Join(root, filepath.Dir(f)), 0o755)
		_ = os.WriteFile(filepath.Join(root, f), []byte("x\n"), 0o644)
	}
	if exec.Command("git", "-C", root, "init", "-q").Run() != nil {
		t.Skip("git not available")
	}
	m := chatModel(t, root)
	typeKeys(m, "fix @")
	run(m, m.buildIndex()) // indexing runs in the background in the app
	m.ment.building = false
	run(m, m.buildIndex())
	rows, _ := m.mentionSugs()
	if len(rows) == 0 || rows[0].Path != "src" {
		t.Fatalf("top level rows = %+v", rows)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyTab}) // open src/
	if v := m.input.Value(); v != "fix @src/" {
		t.Fatalf("after tab = %q", v)
	}
	typeKeys(m, "au/log")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // choose, don't send
	if v := m.input.Value(); v != "fix @src/auth/login.ts " {
		t.Fatalf("after enter = %q", v)
	}
	if m.turn.active || len(m.itemEntries("user")) != 0 {
		t.Fatal("Enter on the picker sent the message")
	}
	if rows, _ := m.mentionSugs(); rows != nil {
		t.Fatal("picker still open after choosing")
	}
	typeKeys(m, "now")
	view := highlightInput(m.input.View())
	if !strings.Contains(view, sMention.Render("@src/auth/login.ts")) {
		t.Fatalf("mention not highlighted in the input: %q", view)
	}
	// e-mail addresses never open the picker
	m.input.SetValue("mail a@b")
	if rows, _ := m.mentionSugs(); rows != nil {
		t.Fatal("picker opened inside an e-mail address")
	}
}

func TestMouseSelectionCopiesText(t *testing.T) {
	m := chatModel(t)
	m.entries = nil
	m.pushItem(session.Item{Kind: "user", Text: "hello there"})
	m.pushItem(session.Item{Kind: "note", Text: "second line here"})
	m.View()
	row := -1
	for i, l := range m.lines {
		if strings.Contains(ansi.Strip(l), "hello there") {
			row = i - m.vp.YOffset
		}
	}
	if row < 0 {
		t.Fatalf("message not drawn: %q", m.lines)
	}
	plain := ansi.Strip(m.lines[m.vp.YOffset+row])
	col := ansi.StringWidth(plain[:strings.Index(plain, "hello")]) // cells, not bytes
	m.Update(tea.MouseMsg{X: col, Y: row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m.Update(tea.MouseMsg{X: col + 4, Y: row, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	if got := m.selectedText(); got != "hello" {
		t.Fatalf("selected %q", got)
	}
	if v := m.View(); !strings.Contains(v, sSelected.Render("hello")) {
		t.Fatal("selection not highlighted")
	}
	_, cmd := m.Update(tea.MouseMsg{X: col + 4, Y: row, Action: tea.MouseActionRelease})
	if cmd == nil {
		t.Fatal("release didn't copy")
	}
	// a plain click selects nothing
	m.Update(tea.MouseMsg{X: 1, Y: row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	_, cmd = m.Update(tea.MouseMsg{X: 1, Y: row, Action: tea.MouseActionRelease})
	if cmd != nil || m.sel.on {
		t.Fatal("a click made a selection")
	}
}

func TestResumeRestoresConversation(t *testing.T) {
	dir := t.TempDir()
	m := chatModel(t, dir)
	m.pushItem(session.Item{Kind: "user", Text: "explain the parser"})
	m.pushItem(session.Item{Kind: "tool", Call: "Read", Args: "parser.go", Text: "Read 40 lines"})
	m.pushItem(session.Item{Kind: "assistant", Text: "It is a **recursive descent** parser."})
	id := m.sess.id
	run(m, m.saveSession())
	if _, err := session.Load(id); err != nil {
		t.Fatalf("not saved: %v", err)
	}
	run(m, m.command("/clear"))
	if m.sess.id == id {
		t.Fatal("/clear kept the conversation id")
	}
	run(m, m.openResume("recursive"))
	if m.state != stateResume || len(m.resumeRows()) != 1 {
		t.Fatalf("resume list: state %v, rows %d", m.state, len(m.resumeRows()))
	}
	if !strings.Contains(ansi.Strip(m.View()), "explain the parser") {
		t.Fatal("list doesn't show the conversation")
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	run(m, cmd)
	if m.sess.id != id || len(m.itemEntries("user", "assistant", "tool")) != 3 {
		t.Fatalf("resumed id %s, items %d", m.sess.id, len(m.itemEntries("user", "assistant", "tool")))
	}
	if !strings.Contains(ansi.Strip(m.View()), "recursive descent") {
		t.Fatal("transcript not restored")
	}
}

func TestCodeBlocksAndCopyPicker(t *testing.T) {
	md := "Run this:\n\n```bash\ngo test ./...\n```\n\nand\n\n~~~go\nfunc a() {}\nfunc b() {}\n~~~\n"
	bs := codeBlocks(md)
	if len(bs) != 2 || bs[0].lang != "bash" || bs[0].code != "go test ./..." || bs[1].lines != 2 {
		t.Fatalf("blocks = %+v", bs)
	}
	m := chatModel(t)
	m.pushItem(session.Item{Kind: "assistant", Text: md})
	m.command("/copy")
	if m.state != stateCopy || len(m.cp.opts) != 3 {
		t.Fatalf("picker: state %v opts %d", m.state, len(m.cp.opts))
	}
	if !strings.Contains(m.exportMarkdown(), "## werkz") {
		t.Fatal("export missing the response")
	}
}
