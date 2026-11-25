package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/tools"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// ---------------------------------------------------------------- activation

// activateWorkspace switches the session to a saved workspace.
func (m *model) activateWorkspace(ws *config.Workspace) tea.Cmd {
	var env []string
	keys := make([]string, 0, len(ws.Env))
	for k := range ws.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+ws.Env[k])
	}
	cmd := m.activateDirs(ws.Dirs, ws.Name, env)
	if name, _, _ := m.eng.Workspace(); name == ws.Name {
		m.ws = ws
		ws.LastUsed = time.Now()
		m.saveConfig()
	}
	return cmd
}

// activateDirs sets the working folders (first = main). name may be empty for an unsaved set.
func (m *model) activateDirs(dirs []string, name string, env []string) tea.Cmd {
	var ok, missing []string
	for _, d := range dirs {
		abs, err := filepath.Abs(tools.Expand(d))
		if st, err2 := os.Stat(abs); err != nil || err2 != nil || !st.IsDir() {
			missing = append(missing, d)
			continue
		}
		ok = append(ok, abs)
	}
	if len(ok) == 0 {
		return m.printS(renderNotice("error", "None of these folders exist: "+strings.Join(dirs, ", ")))
	}
	if err := os.Chdir(ok[0]); err != nil {
		return m.printS(renderNotice("error", err.Error()))
	}
	m.ws = nil
	m.eng.SetWorkspace(ok, env, name)
	label := name
	if label == "" {
		label = "folders"
	}
	msg := fmt.Sprintf("Workspace %s · %d folder(s) · main: %s", label, len(ok), tildify(ok[0]))
	if len(env) > 0 {
		msg += fmt.Sprintf(" · %d env var(s)", len(env))
	}
	cmds := []tea.Cmd{m.printS(renderNotice("info", msg))}
	if len(missing) > 0 {
		cmds = append(cmds, m.printS(renderNotice("warn", "Skipped missing folders: "+strings.Join(missing, ", "))))
	}
	return tea.Batch(cmds...)
}

func tildify(p string) string {
	if h, err := os.UserHomeDir(); err == nil && (p == h || strings.HasPrefix(p, h+string(filepath.Separator))) {
		return "~" + strings.TrimPrefix(p, h)
	}
	return p
}

// clearWorkspace returns to the default: no workspace, just the folder werkz was started in.
func (m *model) clearWorkspace() tea.Cmd {
	m.ws = nil
	m.eng.SetWorkspace(nil, nil, "")
	_ = os.Chdir(m.startDir)
	return m.printS(renderNotice("info", "No workspace · working in "+tildify(m.startDir)))
}

// folderLabel is the status-bar text for where werkz is working.
func (m *model) folderLabel() string {
	if m.eng != nil {
		name, n, _ := m.eng.Workspace()
		if name != "" {
			return fmt.Sprintf("▸ %s (%d)", name, n)
		}
		if n > 1 {
			return fmt.Sprintf("▸ %s +%d", cwdLabel(), n-1)
		}
	}
	return "▸ " + cwdLabel()
}

func (m *model) dirsText() string {
	var b strings.Builder
	title := "Working folders"
	name, _, env := m.eng.Workspace()
	if name != "" {
		title = "Workspace · " + name
	}
	b.WriteString(gradient(title, 0) + "\n")
	for i, d := range m.eng.Dirs() {
		tag := ""
		if i == 0 {
			tag = sPink.Render("  main")
		}
		b.WriteString(fmt.Sprintf("  %s %s%s\n", sDim.Render(fmt.Sprintf("%d.", i+1)), sText.Render(tildify(d)), tag))
	}
	if len(env) > 0 {
		var keys []string
		for _, kv := range env {
			keys = append(keys, strings.SplitN(kv, "=", 2)[0])
		}
		b.WriteString(sDim.Render("  env: ") + sText.Render(strings.Join(keys, ", ")) + "\n")
	}
	b.WriteString(sFaint.Render("  /add-dir <path> adds a folder · /workspace switches · /workspace new creates one"))
	return box(b.String(), cSky, m.w)
}

// addDir adds a folder to the session, and to the active workspace if one is open.
func (m *model) addDir(path string) tea.Cmd {
	abs, err := filepath.Abs(tools.Expand(path))
	if err != nil {
		return m.printS(renderNotice("error", err.Error()))
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return m.printS(renderNotice("error", "Not a folder: "+path))
	}
	dirs := m.eng.Dirs()
	for _, d := range dirs {
		if d == abs {
			return m.printS(renderNotice("warn", tildify(abs)+" is already a working folder"))
		}
	}
	dirs = m.eng.AddDir(abs)
	saved := ""
	if m.ws != nil {
		m.ws.Dirs = append(m.ws.Dirs, abs)
		m.saveConfig()
		saved = " (saved to " + m.ws.Name + ")"
	}
	return m.printS(renderNotice("info", fmt.Sprintf("Added %s · %d folders%s", tildify(abs), len(dirs), saved)))
}

// ---------------------------------------------------------------- picker

type wsItem struct {
	kind    string // ws | here | new
	ws      *config.Workspace
	section string
}

type wsPicker struct {
	filter     textinput.Model
	items      []wsItem
	sel        int
	startup    bool
	confirmDel bool
}

func (m *model) openWsPicker(startup bool) tea.Cmd {
	ti := textinput.New()
	ti.Placeholder = "search workspaces…"
	ti.Prompt = "⌕ "
	ti.PromptStyle = sViolet
	m.wsp = wsPicker{filter: ti, startup: startup}
	m.buildWsItems()
	m.state = stateWorkspaces
	return m.wsp.filter.Focus()
}

func (m *model) buildWsItems() {
	q := strings.ToLower(m.wsp.filter.Value())
	match := func(w *config.Workspace) bool {
		return q == "" || strings.Contains(strings.ToLower(w.Name+" "+strings.Join(w.Dirs, " ")), q)
	}
	var starred, rest []*config.Workspace
	for _, w := range m.cfg.Workspaces {
		if !match(w) {
			continue
		}
		if w.Starred {
			starred = append(starred, w)
		} else {
			rest = append(rest, w)
		}
	}
	sort.SliceStable(rest, func(i, j int) bool { return rest[i].LastUsed.After(rest[j].LastUsed) })
	var items []wsItem
	for _, w := range starred {
		items = append(items, wsItem{kind: "ws", ws: w, section: "Starred"})
	}
	for _, w := range rest {
		items = append(items, wsItem{kind: "ws", ws: w, section: "Workspaces"})
	}
	items = append([]wsItem{{kind: "here", section: "Default"}}, items...)
	items = append(items, wsItem{kind: "new", section: ""})
	m.wsp.items = items
	if m.wsp.sel >= len(items) {
		m.wsp.sel = len(items) - 1
	}
}

func (m *model) wsPickerKey(k tea.KeyMsg) tea.Cmd {
	p := &m.wsp
	it := p.items[p.sel]
	if k.String() != "ctrl+x" {
		p.confirmDel = false
	}
	switch k.String() {
	case "esc":
		m.state = stateChat
		return nil
	case "up", "ctrl+p":
		p.sel = (p.sel - 1 + len(p.items)) % len(p.items)
		return nil
	case "down", "ctrl+n":
		p.sel = (p.sel + 1) % len(p.items)
		return nil
	case "ctrl+s": // star / unstar
		if it.kind == "ws" {
			it.ws.Starred = !it.ws.Starred
			m.saveConfig()
			m.buildWsItems()
		}
		return nil
	case "ctrl+e":
		if it.kind == "ws" {
			return m.openWsForm(it.ws)
		}
		return nil
	case "ctrl+x":
		if it.kind == "ws" {
			if !p.confirmDel {
				p.confirmDel = true
				return nil
			}
			for i, w := range m.cfg.Workspaces {
				if w == it.ws {
					m.cfg.Workspaces = append(m.cfg.Workspaces[:i], m.cfg.Workspaces[i+1:]...)
					break
				}
			}
			p.confirmDel = false
			m.saveConfig()
			m.buildWsItems()
		}
		return nil
	case "enter":
		m.state = stateChat
		switch it.kind {
		case "ws":
			return m.activateWorkspace(it.ws)
		case "new":
			return m.openWsForm(nil)
		case "here":
			return m.clearWorkspace()
		}
		return nil
	}
	var cmd tea.Cmd
	p.filter, cmd = p.filter.Update(k)
	p.sel = 0
	m.buildWsItems()
	return cmd
}

func (m *model) wsPickerView() string {
	p := &m.wsp
	var b strings.Builder
	head := "Choose workspace"
	b.WriteString(gradient(head, 0) + sDim.Render("  ·  enter open · ctrl+s star · ctrl+e edit · ctrl+x delete · esc close") + "\n")
	b.WriteString(p.filter.View() + "\n")
	rows := max(m.h-14, 4)
	start := max(0, p.sel-rows+1)
	section := ""
	for i := start; i < len(p.items) && i < start+rows; i++ {
		it := p.items[i]
		if it.section != section {
			section = it.section
			if section != "" {
				b.WriteString(sFaint.Render(section) + "\n")
			} else {
				b.WriteString(sFaint.Render(strings.Repeat("─", max(min(m.w-6, 50), 10))) + "\n")
			}
		}
		cursor := "  "
		if i == p.sel {
			cursor = sPink.Render("❯ ")
		}
		var name, detail string
		switch it.kind {
		case "ws":
			name = it.ws.Name
			if it.ws.Starred {
				name = "★ " + name
			}
			detail = fmt.Sprintf("%d folder(s) · %s", len(it.ws.Dirs), tildify(firstOr(it.ws.Dirs, "")))
			if name, _, _ := m.eng.Workspace(); name == it.ws.Name {
				detail += "  ✓"
			}
		case "here":
			name, detail = "No workspace", tildify(m.startDir)+"  (default)"
			if name, n, _ := m.eng.Workspace(); name == "" && n <= 1 {
				detail += "  ✓"
			}
		case "new":
			name = "+ New workspace"
		}
		if i == p.sel {
			name = sPink.Bold(true).Render(padRight(name, 26))
		} else {
			name = sText.Render(padRight(name, 26))
		}
		b.WriteString(cursor + name + " " + sDim.Render(detail) + "\n")
	}
	if p.confirmDel {
		b.WriteString(sRed.Render("Press ctrl+x again to delete " + p.items[p.sel].ws.Name))
	}
	return box(strings.TrimRight(b.String(), "\n"), cViolet, m.w)
}

func firstOr(s []string, d string) string {
	if len(s) > 0 {
		return s[0]
	}
	return d
}

// ---------------------------------------------------------------- new / edit form

type wsForm struct {
	editing *config.Workspace
	name    textinput.Model
	dir     textinput.Model
	env     textinput.Model
	dirs    []string
	envs    []string
	focus   int // 0 name · 1 folder · 2 env
	err     string
	cands   []string
}

func newInput(ph string) textinput.Model {
	ti := textinput.New()
	ti.Placeholder = ph
	ti.Prompt = ""
	ti.PlaceholderStyle = sFaint
	ti.TextStyle = sText
	return ti
}

func (m *model) openWsForm(edit *config.Workspace) tea.Cmd {
	f := wsForm{editing: edit, name: newInput("My app"), dir: newInput("~/Desktop/project  (tab completes)"),
		env: newInput("KEY=value")}
	if edit != nil {
		f.name.SetValue(edit.Name)
		f.dirs = append([]string{}, edit.Dirs...)
		for k, v := range edit.Env {
			f.envs = append(f.envs, k+"="+v)
		}
		sort.Strings(f.envs)
	} else if wd, err := os.Getwd(); err == nil {
		if h, _ := os.UserHomeDir(); wd != h {
			f.dirs = []string{wd} // start from the current folder when it is a project
		}
	}
	m.wsf = f
	m.state = stateWsForm
	return m.wsf.name.Focus()
}

func (f *wsForm) inputs() []*textinput.Model { return []*textinput.Model{&f.name, &f.dir, &f.env} }

func (f *wsForm) setFocus(i int) tea.Cmd {
	in := f.inputs()
	in[f.focus].Blur()
	f.focus = (i + 3) % 3
	f.cands = nil
	return in[f.focus].Focus()
}

func (m *model) wsFormKey(k tea.KeyMsg) tea.Cmd {
	f := &m.wsf
	f.err = ""
	switch k.String() {
	case "esc":
		m.state = stateChat
		return nil
	case "ctrl+s":
		return m.saveWsForm()
	case "shift+tab", "up":
		return f.setFocus(f.focus - 1)
	case "down":
		return f.setFocus(f.focus + 1)
	case "tab":
		if f.focus == 1 && f.dir.Value() != "" {
			v, cands := completePath(f.dir.Value())
			f.dir.SetValue(v)
			f.dir.CursorEnd()
			f.cands = cands
			return nil
		}
		return f.setFocus(f.focus + 1)
	case "ctrl+x":
		if f.focus == 1 && len(f.dirs) > 0 {
			f.dirs = f.dirs[:len(f.dirs)-1]
		} else if f.focus == 2 && len(f.envs) > 0 {
			f.envs = f.envs[:len(f.envs)-1]
		}
		return nil
	case "enter":
		switch f.focus {
		case 0:
			return f.setFocus(1)
		case 1:
			v := strings.TrimSpace(f.dir.Value())
			if v == "" {
				return f.setFocus(2)
			}
			abs, err := filepath.Abs(tools.Expand(v))
			if st, err2 := os.Stat(abs); err != nil || err2 != nil || !st.IsDir() {
				f.err = "Not a folder: " + v
				return nil
			}
			for _, d := range f.dirs {
				if d == abs {
					f.err = "Already added"
					return nil
				}
			}
			f.dirs = append(f.dirs, abs)
			f.dir.SetValue("")
			f.cands = nil
			return nil
		case 2:
			v := strings.TrimSpace(f.env.Value())
			if v == "" {
				return m.saveWsForm()
			}
			if kv := strings.SplitN(v, "=", 2); len(kv) != 2 || kv[0] == "" || strings.ContainsAny(kv[0], " \t") {
				f.err = "Use KEY=value"
				return nil
			}
			f.envs = append(f.envs, v)
			f.env.SetValue("")
			return nil
		}
	}
	var cmd tea.Cmd
	in := f.inputs()[f.focus]
	*in, cmd = in.Update(k)
	if f.focus == 1 {
		f.cands = nil
	}
	return cmd
}

func (m *model) saveWsForm() tea.Cmd {
	f := &m.wsf
	name := strings.TrimSpace(f.name.Value())
	if v := strings.TrimSpace(f.dir.Value()); v != "" { // pending folder text counts too
		if abs, err := filepath.Abs(tools.Expand(v)); err == nil {
			if st, err := os.Stat(abs); err == nil && st.IsDir() {
				f.dirs = append(f.dirs, abs)
				f.dir.SetValue("")
			}
		}
	}
	switch {
	case name == "":
		f.err = "Give the workspace a name"
		return f.setFocus(0)
	case len(f.dirs) == 0:
		f.err = "Add at least one folder"
		return f.setFocus(1)
	}
	if other := m.cfg.Workspace(name); other != nil && other != f.editing {
		f.err = "A workspace named " + name + " already exists"
		return f.setFocus(0)
	}
	env := map[string]string{}
	for _, kv := range f.envs {
		p := strings.SplitN(kv, "=", 2)
		env[p[0]] = p[1]
	}
	ws := f.editing
	if ws == nil {
		ws = &config.Workspace{}
		m.cfg.Workspaces = append(m.cfg.Workspaces, ws)
	}
	ws.Name, ws.Dirs, ws.Env = name, append([]string{}, f.dirs...), env
	if len(env) == 0 {
		ws.Env = nil
	}
	m.saveConfig()
	m.state = stateChat
	return m.activateWorkspace(ws)
}

func (m *model) wsFormView() string {
	f := &m.wsf
	title := "New workspace"
	if f.editing != nil {
		title = "Edit workspace"
	}
	fieldW := max(min(m.w-8, 60), 16)
	field := func(i int, in textinput.Model) string {
		c := cFaint
		if f.focus == i {
			c = cViolet
		}
		in.Width = fieldW - 2
		return fg(c).Render("▏") + in.View()
	}
	label := func(i int, s string) string {
		if f.focus == i {
			return sPink.Bold(true).Render(s)
		}
		return sText.Render(s)
	}
	var b strings.Builder
	b.WriteString(gradient(title, 0) + "\n\n")
	b.WriteString(label(0, "Name") + "\n" + field(0, f.name) + "\n\n")
	b.WriteString(label(1, "Folders") + sDim.Render("  first one is the main folder · enter adds · ctrl+x removes last") + "\n")
	for i, d := range f.dirs {
		tag := ""
		if i == 0 {
			tag = sPink.Render("  main")
		}
		b.WriteString(sDim.Render(fmt.Sprintf("  %d. ", i+1)) + sText.Render(tildify(d)) + tag + "\n")
	}
	b.WriteString(field(1, f.dir) + "\n")
	if len(f.cands) > 1 {
		b.WriteString(sDim.Render("  "+strings.Join(f.cands, "  ")) + "\n")
	}
	b.WriteString("\n" + label(2, "Environment variables") + sDim.Render("  optional · for shell commands") + "\n")
	for _, kv := range f.envs {
		p := strings.SplitN(kv, "=", 2)
		b.WriteString(sDim.Render("  • ") + sText.Render(p[0]) + sDim.Render("=••••") + "\n")
	}
	b.WriteString(field(2, f.env) + "\n\n")
	if f.err != "" {
		b.WriteString(sRed.Render(f.err) + "\n")
	}
	action := "Create workspace"
	if f.editing != nil {
		action = "Save workspace"
	}
	b.WriteString(sDim.Render("tab/↑↓ move · ") + sPink.Render("ctrl+s "+action) + sDim.Render(" · esc cancel"))
	return box(b.String(), cViolet, m.w)
}

// completePath completes a folder path; returns the new value and the candidates when ambiguous.
func completePath(v string) (string, []string) {
	exp := tools.Expand(v)
	dir, base := filepath.Split(exp)
	if dir == "" {
		dir = "."
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return v, nil
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() || (strings.HasPrefix(e.Name(), ".") && !strings.HasPrefix(base, ".")) {
			continue
		}
		if strings.HasPrefix(e.Name(), base) {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return v, nil
	}
	prefix := names[0]
	for _, n := range names[1:] {
		for !strings.HasPrefix(n, prefix) {
			prefix = prefix[:len(prefix)-1]
		}
	}
	head := v[:len(v)-len(base)] // keep the user's own spelling (e.g. ~/)
	if len(names) == 1 {
		return head + names[0] + "/", nil
	}
	if len(names) > 8 {
		names = append(names[:8], "…")
	}
	return head + prefix, names
}

func (m *model) wsBannerLine(wd string) string {
	if m.eng != nil {
		if name, n, _ := m.eng.Workspace(); name != "" {
			return sDim.Render("workspace: ") + sSky.Render(name) + sDim.Render(fmt.Sprintf(" · %d folders", n))
		}
	}
	return sDim.Render(wd) + sFaint.Render("  · /workspace to open several folders")
}
