package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TonmoyTalukder/werkz/internal/agent"
	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/tools"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestViewsRender(t *testing.T) {
	os.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Load()
	m := newModel(cfg)
	m.eng = agent.New(cfg, func(any) {})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	dump := os.Getenv("WERKZ_DUMP") != ""
	show := func(name, s string) {
		if s == "" {
			t.Fatalf("%s view empty", name)
		}
		if dump {
			fmt.Printf("\n===== %s =====\n%s\n", name, s)
		}
	}
	show("login", m.View())

	cfg.Keys["anthropic"] = "sk-ant-x"
	cfg.Onboarded = true
	m.state = stateChat
	m.ensureProvider(true)
	if cfg.Provider != "anthropic" {
		t.Fatalf("provider after login = %s, want anthropic", cfg.Provider)
	}
	show("banner", m.banner(120))
	m.stats.ctxUsed = 120_000
	m.stats.cost = 0.0421
	cfg.Balance = 100
	show("chat", m.View())

	m.turn.active = true
	m.turn.verb = "Refracting"
	m.turn.curProv = "anthropic"
	m.turn.thinking.WriteString("The user wants a list of files. I'll call LS.")
	m.turn.live.WriteString("Here is what I found:\n\n- main.go\n- ui.go")
	m.turn.pending = &pendingTool{Name: "Bash", Summary: "go test ./..."}
	show("running", m.View())
	m.turn.active = false

	m.command("/model")
	show("model picker", m.View())
	m.state = stateChat
	m.command("/effort")
	show("effort", m.View())
	m.state = stateChat
	reply := make(chan agent.PermDecision, 1)
	m.Update(agent.PermReqMsg{Tool: "Edit", Summary: "main.go", Detail: tools.DiffLines("old line", "new line\nsecond", 10), Reply: reply})
	show("permission", m.View())
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	if d := <-reply; d != agent.PermAlways {
		t.Fatalf("perm decision %v", d)
	}
	show("tool result", renderToolResult(agent.ToolResultMsg{Name: "Edit", Summary: "+2 −1 lines", Detail: tools.DiffLines("a", "b\nc", 8)}, "Edit(main.go)", 120))
	show("assistant", m.renderAssistant("**Done.** Updated `main.go`:\n\n```go\nfmt.Println(\"hi\")\n```", 120))
	show("cost", m.costText())
	show("fallback", m.fallbackText())
	m.input.SetValue("/")
	show("suggestions", m.View())
}

// Every screen must fit the window exactly at any size: no line wider than the
// terminal, and no more lines than its height.
func TestResponsiveSizes(t *testing.T) {
	os.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Load()
	cfg.Keys["anthropic"] = "sk-ant-x"
	cfg.Onboarded = true
	m := newModel(cfg)
	m.eng = agent.New(cfg, func(any) {})
	m.Init()
	m.push(func(w int) string {
		return renderUser("a fairly long user message that should wrap nicely on narrow screens", w)
	})
	m.push(func(w int) string {
		return m.renderAssistant("## Heading\n\nSome **markdown** with a long paragraph that needs wrapping when the window is narrow.", w)
	})
	m.push(func(w int) string {
		return renderToolResult(agent.ToolResultMsg{Name: "Edit", Summary: "+1 −1", Detail: tools.DiffLines(strings.Repeat("x", 200), "y", 8)}, "Edit(some/very/long/path/to/a/file.go)", w)
	})
	check := func(name string) {
		v := m.View()
		lines := strings.Split(v, "\n")
		for i, l := range lines {
			if lw := lipgloss.Width(l); lw > m.w {
				t.Errorf("%s %dx%d: line %d is %d wide", name, m.w, m.h, i, lw)
			}
		}
		if m.state != stateLogin && len(lines) != m.h {
			t.Errorf("%s %dx%d: %d lines", name, m.w, m.h, len(lines))
		}
	}
	for _, sz := range [][2]int{{40, 15}, {60, 20}, {80, 24}, {100, 30}, {140, 40}, {220, 60}} {
		m.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		m.state = stateChat
		check("chat")
		m.turn.active, m.turn.verb, m.turn.curProv = true, "Refracting", "anthropic"
		m.turn.live.WriteString("streaming text that goes on and on and on across the screen width")
		check("running")
		m.turn.active = false
		m.turn.live.Reset()
		m.command("/model")
		check("picker")
		m.state = stateChat
		m.command("/effort")
		check("effort")
		m.state = stateLogin
		m.login = newLogin(cfg, true)
		v := m.View()
		for i, l := range strings.Split(v, "\n") {
			if lw := lipgloss.Width(l); lw > m.w {
				t.Errorf("login %dx%d: line %d is %d wide", m.w, m.h, i, lw)
			}
		}
		if n := len(strings.Split(v, "\n")); n > m.h {
			t.Errorf("login %dx%d: %d lines", m.w, m.h, n)
		}
	}
}

func TestWorkspaces(t *testing.T) {
	os.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a, b := t.TempDir(), t.TempDir()
	must(t, os.WriteFile(filepath.Join(a, "one.go"), []byte("package a // NEEDLE\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(b, "two.go"), []byte("package b // NEEDLE\n"), 0o644))
	must(t, os.MkdirAll(filepath.Join(a, "services", "api"), 0o755))
	must(t, os.MkdirAll(filepath.Join(a, "scripts"), 0o755))

	cfg := config.Load()
	cfg.Keys["anthropic"] = "sk-ant-x"
	cfg.Onboarded = true
	m := newModel(cfg)
	m.eng = agent.New(cfg, func(any) {})
	start := m.startDir

	// default: no workspace
	if name, n := wsState(m); name != "" || n != 0 {
		t.Fatal("should start without a workspace")
	}

	// create one through the form
	m.openWsForm(nil)
	m.wsf.name.SetValue("both")
	m.wsf.dirs = nil
	m.wsf.dir.SetValue(a)
	m.wsFormKey(tea.KeyMsg{Type: tea.KeyEnter}) // focus is on name → moves to folder
	m.wsFormKey(tea.KeyMsg{Type: tea.KeyEnter}) // adds a
	m.wsf.dir.SetValue(b)
	m.wsFormKey(tea.KeyMsg{Type: tea.KeyEnter}) // adds b
	m.wsf.env.SetValue("APP_ENV=dev")
	m.wsf.focus = 2
	m.wsFormKey(tea.KeyMsg{Type: tea.KeyEnter}) // adds env
	m.wsFormKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	if name, n := wsState(m); name != "both" || n != 2 || cfg.Workspace("both") == nil {
		t.Fatalf("workspace not active: %s %d %v", name, n, m.wsf.err)
	}
	if wd, _ := os.Getwd(); wd != a {
		t.Fatalf("cwd = %s, want main folder %s", wd, a)
	}

	// path completion
	if v, _ := completePath(filepath.Join(a, "se")); v != filepath.Join(a, "services")+"/" {
		t.Fatalf("complete = %q", v)
	}
	if _, c := completePath(filepath.Join(a, "s")); len(c) != 2 {
		t.Fatalf("candidates = %v", c)
	}

	// back to default
	m.command("/workspace none")
	if name, n := wsState(m); name != "" || n != 0 {
		t.Fatal("workspace not cleared")
	}
	if wd, _ := os.Getwd(); wd != start {
		t.Fatalf("cwd not restored: %s", wd)
	}
	m.command("/workspace both")
	if name, _ := wsState(m); name != "both" {
		t.Fatal("/workspace <name> failed")
	}
	m.command("/workspace none")

	// picker & form fit small windows
	for _, sz := range [][2]int{{40, 15}, {80, 24}, {160, 45}} {
		m.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		for _, open := range []func(){func() { m.openWsPicker(false) }, func() { m.openWsForm(cfg.Workspace("both")) }} {
			open()
			v := m.View()
			for i, l := range strings.Split(v, "\n") {
				if lw := lipgloss.Width(l); lw > m.w {
					t.Errorf("%dx%d state %d line %d width %d", m.w, m.h, m.state, i, lw)
				}
			}
			if n := len(strings.Split(v, "\n")); n != m.h {
				t.Errorf("%dx%d state %d: %d lines", m.w, m.h, m.state, n)
			}
		}
	}
	if os.Getenv("WERKZ_DUMP") != "" {
		m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
		m.openWsPicker(false)
		fmt.Println(m.View())
		m.openWsForm(cfg.Workspace("both"))
		fmt.Println(m.View())
	}
}

func TestFallbackChain(t *testing.T) {
	os.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Load()
	cfg.Keys["anthropic"] = "k"
	cfg.Keys["openrouter"] = "k"
	cfg.Provider, cfg.Model = "openrouter", "baidu/ernie-5.5-flash-preview:free"
	cfg.FallbackChain = []config.FallbackModel{
		{Provider: "openrouter", Model: "meta-llama/llama-3.3-70b-instruct:free"},
		{Provider: "anthropic", Model: "claude-haiku-5-5"},
		{Provider: "gemini", Model: "gemini-2.5-flash"}, // no key → skipped
	}
	e := agent.New(cfg, func(any) {})

	// UI: Auto → model chooser → pick two → saved in order
	m := newModel(cfg)
	m.eng = e
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.command("/fallback")
	m.fbModeSel = 2 // Auto
	m.fallbackModeKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.state != stateFallbackChain {
		t.Fatal("choosing Auto should open the model chooser")
	}
	m.fbc.chosen = nil
	for i, mi := range m.fbc.shown {
		if mi.ID == "claude-haiku-5-5" || mi.ID == "claude-sonnet-5-5" {
			m.fbc.sel = i
			m.fallbackChainKey(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")})
		}
	}
	view := m.View()
	if !strings.Contains(view, "[1]") || !strings.Contains(view, "[2]") {
		t.Fatal("chooser should number the picked models")
	}
	m.fallbackChainKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cfg.Fallback() != "auto" || len(cfg.FallbackChain) != 2 || cfg.FallbackChain[0].Model != "claude-sonnet-5-5" {
		t.Fatalf("saved: mode=%s chain=%+v", cfg.Fallback(), cfg.FallbackChain)
	}
}

func TestFallbackDialog(t *testing.T) {
	os.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Load()
	cfg.Keys["anthropic"] = "k"
	cfg.Onboarded = true
	m := newModel(cfg)
	m.eng = agent.New(cfg, func(any) {})
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
	ask := func() chan agent.FallbackChoice {
		r := make(chan agent.FallbackChoice, 1)
		m.Update(agent.FallbackAskMsg{From: "openrouter", FromModel: "x:free", Reason: "HTTP 429: rate limited",
			Candidates: []config.FallbackModel{{Provider: "anthropic", Model: "claude-haiku-5-5"}},
			Exclude:    []config.FallbackModel{{Provider: "anthropic", Model: "claude-opus-5-5"}}, Reply: r})
		return r
	}
	// 1 = first suggested model, for this message
	r := ask()
	if v := m.View(); !strings.Contains(v, "Switch to which model?") || !strings.Contains(v, "Haiku 5.5") || !strings.Contains(v, "Choose another model") {
		t.Fatal("dialog should list the suggestions and 'Choose another model'")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	if c := <-r; c.Model != "claude-haiku-5-5" || c.Session {
		t.Fatalf("choice = %+v", c)
	}
	// browse: excluded (already failed) models are not offered; ctrl+a = whole session
	r = ask()
	m.fbSel = 1
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.fbBrowse {
		t.Fatal("'Choose another model' should open the list")
	}
	for _, mi := range m.fbList.shown {
		if mi.ID == "claude-opus-5-5" {
			t.Fatal("excluded model offered")
		}
	}
	m.fbList.filter.SetValue("sonnet")
	m.fbList.apply()
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if c := <-r; c.Model != "claude-sonnet-5-5" || !c.Session {
		t.Fatalf("browse choice = %+v", c)
	}
	// esc = stop
	r = ask()
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if c := <-r; !c.Stop {
		t.Fatal("esc should stop")
	}
}

func wsState(m *model) (name string, folders int) {
	name, folders, _ = m.eng.Workspace()
	return name, folders
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenRouterPaidToggle(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Load()
	cfg.Keys["openrouter"] = "k"
	cfg.Onboarded = true
	m := newModel(cfg)
	m.eng = agent.New(cfg, func(any) {})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if cfg.OpenRouterPaid {
		t.Fatal("paid OpenRouter models must be off by default")
	}
	m.command("/model")
	if !strings.Contains(m.View(), "/openrouter paid") {
		t.Fatal("model picker should say paid OpenRouter models are hidden")
	}
	m.state = stateChat
	m.command("/openrouter paid")
	if !cfg.OpenRouterPaid || !config.Load().OpenRouterPaid {
		t.Fatal("/openrouter paid should enable and save the option")
	}
	m.command("/openrouter free")
	if cfg.OpenRouterPaid {
		t.Fatal("/openrouter free should turn it off")
	}
}
