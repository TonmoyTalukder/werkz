// Command werkz is an agentic coding assistant for the terminal.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/TonmoyTalukder/werkz/internal/agent"
	"github.com/TonmoyTalukder/werkz/internal/catalog"
	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/tools"
	"github.com/TonmoyTalukder/werkz/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// version is set at build time: go build -ldflags "-X main.version=1.2.3".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version")
	login := flag.Bool("login", false, "open the API key screen on start")
	modelFlag := flag.String("model", "", "model id to use, e.g. claude-sonnet-5-5")
	wsFlag := flag.String("w", "", "open a saved workspace by name (default: no workspace)")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: werkz [flags] [folder ...]")
		fmt.Fprintln(os.Stderr, "  folders: work in these folders (first is the main one); default is the current folder")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Println("werkz", version)
		return
	}
	if err := run(*login, *modelFlag, *wsFlag, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "werkz:", err)
		os.Exit(1)
	}
}

func run(login bool, model, workspace string, dirs []string) error {
	// Use dark-terminal styles unless WERKZ_THEME=light.
	lipgloss.SetHasDarkBackground(os.Getenv("WERKZ_THEME") != "light")

	cfg := config.Load()
	if model != "" {
		cfg.Provider, cfg.Model = catalog.GuessProvider(model), model
		cfg.Effort = catalog.Lookup(cfg.Provider, cfg.Model).DefaultEffort
	}

	var p *tea.Program
	eng := agent.New(cfg, func(v any) { p.Send(v) })
	m := tui.New(cfg, eng, tui.Options{Version: version, Login: login, Workspace: workspace, Dirs: dirs})
	p = tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	defer tools.KillAll() // don't leave commands started by the agent running after exit
	if _, err := p.Run(); err != nil {
		return err
	}
	return cfg.Save()
}
