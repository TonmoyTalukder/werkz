package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/TonmoyTalukder/werkz/internal/update"
)

const (
	updateEvery   = 24 * time.Hour
	installScript = "curl -fsSL https://raw.githubusercontent.com/TonmoyTalukder/werkz/main/install.sh | sh"
)

// updateMsg is the result of a background update check (and install).
type updateMsg struct {
	Manual    bool   // started with /update (report every outcome)
	Latest    string // newest released version ("" if the check failed)
	Installed bool
	Err       error
}

// autoUpdateDue: automatic updates are on and the last check was more than a day ago.
func (m *model) autoUpdateDue() bool {
	if m.cfg.NoAutoUpdate || os.Getenv("WERKZ_NO_UPDATE") == "1" {
		return false
	}
	if !update.Newer("999.0.0", m.version) { // a "dev" or unversioned build never updates itself
		return false
	}
	return time.Since(m.cfg.UpdateCheckedAt) > updateEvery
}

// checkUpdate looks for a newer release in the background and installs it.
func (m *model) checkUpdate(manual bool) tea.Cmd {
	u, current := *m.updater, m.version // copies: the command runs on another goroutine
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		latest, err := u.Latest(ctx)
		if err != nil {
			return updateMsg{Manual: manual, Err: err}
		}
		if !update.Newer(latest, current) {
			return updateMsg{Manual: manual, Latest: latest}
		}
		err = u.Install(ctx, latest)
		return updateMsg{Manual: manual, Latest: latest, Installed: err == nil, Err: err}
	}
}

func (m *model) handleUpdate(msg updateMsg) tea.Cmd {
	if msg.Latest != "" || msg.Manual {
		m.cfg.UpdateCheckedAt = time.Now()
		m.saveConfig()
	}
	switch {
	case msg.Installed:
		m.updated = msg.Latest
		return m.printS(renderNotice("info", fmt.Sprintf(
			"werkz %s was downloaded and installed. Restart werkz to use it (this session keeps running %s).",
			msg.Latest, m.version)))
	case errors.As(msg.Err, new(*update.ManagedError)):
		var me *update.ManagedError
		errors.As(msg.Err, &me)
		return m.printS(renderNotice("info", fmt.Sprintf("werkz %s is available. You installed werkz with a package manager, so update it with: %s",
			msg.Latest, me.Command)))
	case errors.Is(msg.Err, update.ErrNotWritable):
		return m.printS(renderNotice("warn", fmt.Sprintf(
			"werkz %s is available, but this installation can't update itself. Update with: %s", msg.Latest, installScript)))
	case msg.Err != nil && msg.Latest != "":
		return m.printS(renderNotice("warn", fmt.Sprintf("werkz %s is available, but installing it failed: %v", msg.Latest, msg.Err)))
	case msg.Err != nil && msg.Manual:
		return m.printS(renderNotice("error", "Couldn't check for updates: "+msg.Err.Error()))
	case msg.Manual:
		return m.printS(renderNotice("info", fmt.Sprintf("werkz %s is up to date.", m.version)))
	}
	return nil // automatic check: stay quiet when there's nothing to do or we're offline
}
