// Package tui is werkz's full-screen terminal interface, built on Bubble Tea.
package tui

import (
	"github.com/TonmoyTalukder/werkz/internal/agent"
	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/update"

	tea "github.com/charmbracelet/bubbletea"
)

// Options configure the UI at start-up.
type Options struct {
	Version   string
	Login     bool     // open the API key screen first
	Workspace string   // open this saved workspace
	Dirs      []string // or work in these folders (first is the main one)
	// Updates: Updater replaces the release source/executable (tests); nil = GitHub releases
	// and the running executable.
	Updater *update.Updater
}

// New returns the Bubble Tea model. eng must deliver its events to the same program
// (typically via tea.Program.Send).
func New(cfg *config.Config, eng *agent.Engine, opts Options) tea.Model {
	m := newModel(cfg)
	m.eng, m.version = eng, opts.Version
	m.updater = opts.Updater
	if m.updater == nil {
		m.updater = &update.Updater{}
	}
	eng.SetMode(cfg.Mode)
	if opts.Login {
		m.state = stateLogin
		m.login = newLogin(cfg, false)
	}
	switch {
	case opts.Workspace != "":
		name := opts.Workspace
		m.onStart = func() tea.Cmd {
			if ws := cfg.Workspace(name); ws != nil {
				return m.activateWorkspace(ws)
			}
			return m.printS(renderNotice("warn", "No workspace named "+name+" · starting without one"))
		}
	case len(opts.Dirs) > 0:
		dirs := opts.Dirs
		m.onStart = func() tea.Cmd { return m.activateDirs(dirs, "", nil) }
	}
	return m
}
