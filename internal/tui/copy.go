package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/clipboard"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// ---------------------------------------------------------------- copying

// werkz captures the mouse for scrolling, so the terminal's own selection needs shift (most
// terminals), option (iTerm2) or fn (Terminal.app) while dragging, or ctrl+s to turn the
// mouse off. Copying in werkz itself copies the text as written: markdown, exact code.

type copiedMsg struct {
	what string
	n    int
	res  clipboard.Result
	err  error
}

type copyOpt struct {
	label string
	text  string
	ext   string // file extension for "write to a file"
}

// copyPicker offers a response's parts: the whole response, or one of its code blocks.
type copyPicker struct {
	title string
	opts  []copyOpt
	sel   int
}

// copyText copies text to the clipboard in the background and reports how.
func copyText(what, text string) tea.Cmd {
	return func() tea.Msg {
		res, err := clipboard.Copy(text)
		return copiedMsg{what: what, n: len([]rune(text)), res: res, err: err}
	}
}

func (m *model) handleCopied(msg copiedMsg) tea.Cmd {
	if msg.err != nil {
		return m.printS(renderNotice("warn", "Couldn't copy "+msg.what+": "+msg.err.Error()+
			". Press w in the /copy list to write it to a file, or ctrl+s to select it with your terminal."))
	}
	if !msg.res.Confirmed {
		// OSC 52 is never acknowledged: say what was tried, and the sure ways.
		m.toast = fmt.Sprintf("Sent %s (%s characters) to the terminal's clipboard (OSC 52). If it doesn't paste, your terminal "+
			"ignores that: install wl-clipboard or xclip, or press ctrl+s and select with the terminal.", msg.what, humanInt(msg.n))
		return nil
	}
	m.toast = fmt.Sprintf("✓ Copied %s · %s character%s via %s", msg.what, humanInt(msg.n), plural(msg.n), msg.res.Via)
	return nil
}

func humanInt(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// items returns the conversation items of the given kind, oldest first, with their entries.
func (m *model) itemEntries(kinds ...string) []int {
	var out []int
	for i, e := range m.entries {
		if e.item == nil {
			continue
		}
		for _, k := range kinds {
			if e.item.Kind == k {
				out = append(out, i)
			}
		}
	}
	return out
}

// copyCommand handles /copy [N] · /copy prompt [N] · /copy all.
func (m *model) copyCommand(args []string) tea.Cmd {
	kind, what := "assistant", "response"
	if len(args) > 0 && (args[0] == "prompt" || args[0] == "request") {
		kind, what, args = "user", "request", args[1:]
	}
	if len(args) > 0 && args[0] == "all" {
		md := m.exportMarkdown()
		if md == "" {
			return m.printS(renderNotice("info", "Nothing to copy yet."))
		}
		return copyText("the conversation", md)
	}
	n := 1
	if len(args) > 0 {
		v, err := strconv.Atoi(args[0])
		if err != nil || v < 1 {
			return m.printS(renderNotice("warn", "Usage: /copy [N] · /copy prompt [N] · /copy all (N = 1 for the latest, 2 for the one before…)"))
		}
		n = v
	}
	idx := m.itemEntries(kind)
	if len(idx) == 0 {
		return m.printS(renderNotice("info", "No "+what+" to copy yet."))
	}
	if n > len(idx) {
		return m.printS(renderNotice("warn", fmt.Sprintf("There %s only %d %s%s.", map[bool]string{true: "is", false: "are"}[len(idx) == 1], len(idx), what, plural(len(idx)))))
	}
	return m.copyEntry(idx[len(idx)-n], n == 1)
}

// copyEntry copies a request or response; a response with code blocks opens the picker.
func (m *model) copyEntry(i int, latest bool) tea.Cmd {
	it := m.entries[i].item
	what := "request"
	if it.Kind == "assistant" {
		what = "response"
		if latest {
			what = "the latest response"
		}
		if blocks := codeBlocks(it.Text); len(blocks) > 0 {
			m.openCopyPicker(it.Text, blocks)
			return nil
		}
	}
	return copyText(what, strings.TrimSpace(it.Text))
}

func (m *model) openCopyPicker(text string, blocks []codeBlock) {
	opts := []copyOpt{{label: fmt.Sprintf("Whole response (%d lines, markdown)", strings.Count(strings.TrimSpace(text), "\n")+1), text: strings.TrimSpace(text), ext: "md"}}
	for _, b := range blocks {
		first := strings.TrimSpace(strings.SplitN(b.code, "\n", 2)[0])
		lang := b.lang
		if lang == "" {
			lang = "code"
		}
		opts = append(opts, copyOpt{label: fmt.Sprintf("%s · %s (%d line%s)", lang, trunc(first, 50), b.lines, plural(b.lines)), text: b.code, ext: extFor(b.lang)})
	}
	m.cp = copyPicker{title: "Copy", opts: opts}
	m.prevState = m.state
	m.state = stateCopy
}

func (m *model) copyPickerKey(k tea.KeyMsg) tea.Cmd {
	n := len(m.cp.opts)
	switch k.String() {
	case "esc", "q":
		m.state = m.prevState
		return nil
	case "up", "k", "shift+tab":
		m.cp.sel = (m.cp.sel - 1 + n) % n
	case "down", "j", "tab":
		m.cp.sel = (m.cp.sel + 1) % n
	case "enter", "y":
		o := m.cp.opts[m.cp.sel]
		m.state = m.prevState
		what := "the response"
		if m.cp.sel > 0 {
			what = "the code block"
		}
		return copyText(what, o.text)
	case "w":
		o := m.cp.opts[m.cp.sel]
		m.state = m.prevState
		p, err := writeCopyFile(o.text, o.ext)
		if err != nil {
			return m.printS(renderNotice("error", "Couldn't write the file: "+err.Error()))
		}
		m.toast = "✓ Wrote " + tildify(p)
	default:
		if d, err := strconv.Atoi(k.String()); err == nil && d >= 1 && d <= n {
			m.cp.sel = d - 1
		}
	}
	return nil
}

func (m *model) copyPickerView() string {
	var b strings.Builder
	b.WriteString(gradient("Copy", 0) + sDim.Render("  ·  the response as markdown, or one code block exactly") + "\n")
	for i, o := range m.cp.opts {
		num := fmt.Sprintf("%d. ", i+1)
		if i == m.cp.sel {
			b.WriteString(ansi.Truncate(sPink.Render("❯ "+num)+sPink.Bold(true).Render(o.label), max(m.w-4, 20), "…") + "\n")
		} else {
			b.WriteString(ansi.Truncate("  "+sDim.Render(num)+sText.Render(o.label), max(m.w-4, 20), "…") + "\n")
		}
	}
	b.WriteString(sFaint.Render("  enter copy · w write to a file · ↑↓ or 1–9 choose · esc close"))
	return box(b.String(), cViolet, m.w)
}

func writeCopyFile(text, ext string) (string, error) {
	wd, _ := os.Getwd()
	p := filepath.Join(wd, "werkz-copy-"+time.Now().Format("20060102-150405")+"."+ext)
	return p, os.WriteFile(p, []byte(text+"\n"), 0o644)
}

type codeBlock struct {
	lang, code string
	lines      int
}

// codeBlocks finds the fenced code blocks (``` or ~~~) in markdown.
func codeBlocks(md string) []codeBlock {
	var out []codeBlock
	lines := strings.Split(md, "\n")
	for i := 0; i < len(lines); i++ {
		t := strings.TrimLeft(lines[i], " ")
		fence := ""
		switch {
		case strings.HasPrefix(t, "```"):
			fence = t[:len(t)-len(strings.TrimLeft(t, "`"))]
		case strings.HasPrefix(t, "~~~"):
			fence = t[:len(t)-len(strings.TrimLeft(t, "~"))]
		default:
			continue
		}
		lang := strings.Fields(strings.TrimPrefix(t, fence) + " ")
		b := codeBlock{}
		if len(lang) > 0 {
			b.lang = lang[0]
		}
		var body []string
		j := i + 1
		for ; j < len(lines); j++ {
			if strings.HasPrefix(strings.TrimLeft(lines[j], " "), fence) && strings.TrimSpace(strings.Trim(strings.TrimSpace(lines[j]), fence[:1])) == "" {
				break
			}
			body = append(body, lines[j])
		}
		b.code, b.lines = strings.Join(body, "\n"), len(body)
		if b.lines > 0 {
			out = append(out, b)
		}
		i = j
	}
	return out
}

func extFor(lang string) string {
	switch strings.ToLower(lang) {
	case "go", "py", "js", "ts", "tsx", "jsx", "rs", "rb", "java", "c", "cpp", "h", "sql", "json", "yaml", "toml", "html", "css", "md", "sh", "kt", "swift", "php":
		return strings.ToLower(lang)
	case "python":
		return "py"
	case "javascript":
		return "js"
	case "typescript":
		return "ts"
	case "bash", "shell", "zsh", "console":
		return "sh"
	case "rust":
		return "rs"
	case "yml":
		return "yaml"
	}
	return "txt"
}

// ---------------------------------------------------------------- selecting messages

// selectable entries: requests and responses.
func (m *model) selectable() []int { return m.itemEntries("user", "assistant") }

// startSelect enters message-select mode on the latest response (alt+↑).
func (m *model) startSelect() {
	idx := m.selectable()
	if len(idx) == 0 {
		m.flash = "Nothing to select yet"
		return
	}
	m.selEntry = idx[len(idx)-1]
	m.state = stateSelect
	m.follow = false
}

func (m *model) selectKey(k tea.KeyMsg) tea.Cmd {
	idx := m.selectable()
	pos := 0
	for i, e := range idx {
		if e == m.selEntry {
			pos = i
		}
	}
	switch k.String() {
	case "esc", "q":
		m.state = stateChat
		m.follow = true
	case "up", "k", "alt+up":
		if pos > 0 {
			m.selEntry = idx[pos-1]
		}
	case "down", "j", "alt+down":
		if pos < len(idx)-1 {
			m.selEntry = idx[pos+1]
		}
	case "home", "g":
		m.selEntry = idx[0]
	case "end", "G":
		m.selEntry = idx[len(idx)-1]
	case "enter", "y", "c":
		m.state = stateChat
		m.follow = true
		return m.copyEntry(m.selEntry, false)
	}
	return nil
}

// selectedLine is the transcript line where the selected entry starts.
func (m *model) selectedLine(w int) (start, height int) {
	line := 0
	for i, e := range m.entries {
		if e.w != w {
			e.out, e.w = e.render(w), w
		}
		h := strings.Count(e.out, "\n") + 1
		if i == m.selEntry {
			return line, h
		}
		line += h
	}
	return line, 0
}

// markSelected draws the selection bar beside an entry.
func markSelected(s string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = sPink.Render("▌") + lines[i]
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------- export

// exportMarkdown is the conversation as markdown: requests, answers and the tools used.
func (m *model) exportMarkdown() string {
	var b strings.Builder
	for _, e := range m.entries {
		it := e.item
		if it == nil {
			continue
		}
		switch it.Kind {
		case "user":
			b.WriteString("## You\n\n" + strings.TrimSpace(it.Text) + "\n\n")
		case "assistant":
			b.WriteString("## werkz\n\n" + strings.TrimSpace(it.Text) + "\n\n")
		case "tool":
			call := it.Call
			if it.Args != "" {
				call += "(" + it.Args + ")"
			}
			fmt.Fprintf(&b, "> **%s** · %s\n\n", strings.ReplaceAll(call, "*", `\*`), it.Text)
		case "attach":
			b.WriteString("> attached " + strings.ReplaceAll(it.Text, "\n", " · ") + "\n\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func (m *model) exportCommand(args []string) tea.Cmd {
	md := m.exportMarkdown()
	if md == "" {
		return m.printS(renderNotice("info", "Nothing to export yet."))
	}
	name := ""
	if len(args) > 0 {
		name = strings.Join(args, " ")
	}
	wd, _ := os.Getwd()
	p := name
	if p == "" {
		p = "werkz-conversation-" + time.Now().Format("20060102-150405") + ".md"
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(wd, p)
	}
	if _, err := os.Stat(p); err == nil {
		return m.printS(renderNotice("warn", tildify(p)+" already exists; give another name: /export <file>"))
	}
	title := "werkz conversation"
	if idx := m.itemEntries("user"); len(idx) > 0 {
		title = trunc(strings.Join(strings.Fields(m.entries[idx[0]].item.Text), " "), 80)
	}
	if err := os.WriteFile(p, []byte("# "+title+"\n\n"+md+"\n"), 0o644); err != nil {
		return m.printS(renderNotice("error", "Couldn't export: "+err.Error()))
	}
	return m.printS(renderNotice("info", "Exported the conversation to "+tildify(p)))
}

// ---------------------------------------------------------------- mouse

// toggleMouse turns mouse capture off (so the terminal's own selection works) or back on.
func (m *model) toggleMouse() tea.Cmd {
	m.mouseOff = !m.mouseOff
	if m.mouseOff {
		return tea.DisableMouse
	}
	return tea.EnableMouseCellMotion
}
