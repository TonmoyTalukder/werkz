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

const updateEvery = 24 * time.Hour

// updateMsg is the result of a background update check (and install).
type updateMsg struct {
	Manual    bool   // started with /update (report every outcome)
	Latest    string // newest released version ("" if the check failed)
	Installed bool
	Method    update.Method // how this copy is updated (when Latest is newer)
	Err       error
}

// updateCheckDue: the last look for a new release was more than a day ago. werkz looks even
// when automatic installs are off, so it can say that an update is available.
func (m *model) updateCheckDue() bool {
	if os.Getenv("WERKZ_NO_UPDATE") == "1" {
		return false
	}
	if !update.Newer("999.0.0", m.version) { // a "dev" or unversioned build never updates itself
		return false
	}
	return time.Since(m.cfg.UpdateCheckedAt) > updateEvery
}

// autoUpdateDue: an automatic install may happen now.
func (m *model) autoUpdateDue() bool { return !m.cfg.NoAutoUpdate && m.updateCheckDue() }

// checkUpdate looks for a newer release in the background and, for copies werkz updates
// itself, installs it (always for /update, otherwise only with automatic updates on).
func (m *model) checkUpdate(manual bool) tea.Cmd {
	u, current, auto := *m.updater, m.version, !m.cfg.NoAutoUpdate
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
		method := u.Method(latest)
		if method.Kind != "self" || !(manual || auto) {
			return updateMsg{Manual: manual, Latest: latest, Method: method}
		}
		err = u.Install(ctx, latest)
		return updateMsg{Manual: manual, Latest: latest, Installed: err == nil, Method: method, Err: err}
	}
}

func (m *model) handleUpdate(msg updateMsg) tea.Cmd {
	if msg.Latest != "" || msg.Manual {
		m.cfg.UpdateCheckedAt = time.Now()
		if msg.Latest != "" {
			m.cfg.UpdateLatest = msg.Latest
		}
		m.saveConfig()
	}
	switch {
	case msg.Installed:
		m.updated, m.available = msg.Latest, ""
		return m.printS(renderNotice("info", fmt.Sprintf(
			"werkz %s was downloaded and installed. Restart werkz to use it (this session keeps running %s).",
			msg.Latest, m.version)))
	case msg.Err != nil && msg.Latest != "" && !errors.Is(msg.Err, update.ErrNotWritable):
		m.available = msg.Latest
		return m.printS(renderNotice("warn", fmt.Sprintf("werkz %s is available, but installing it failed: %v. %s",
			msg.Latest, msg.Err, "Update with: "+update.InstallScript)))
	case msg.Err != nil && msg.Latest == "" && msg.Manual:
		return m.printS(renderNotice("error", "Couldn't check for updates: "+msg.Err.Error()))
	case msg.Latest != "" && update.Newer(msg.Latest, m.version):
		m.available = msg.Latest
		if errors.Is(msg.Err, update.ErrNotWritable) {
			msg.Method = update.Method{Kind: "readonly", Command: update.InstallScript}
		}
		return m.printS(renderNotice("info", m.updateAdvice(msg.Latest, msg.Method)))
	case msg.Manual:
		return m.printS(renderNotice("info", fmt.Sprintf("werkz %s is up to date.", m.version)))
	}
	return nil // automatic check: stay quiet when there's nothing to do or we're offline
}

// updateAdvice tells the user that a release is available and the best way to get it for the
// way they installed werkz.
func (m *model) updateAdvice(latest string, how update.Method) string {
	head := fmt.Sprintf("werkz %s is available (you have %s). ", latest, m.version)
	switch how.Kind {
	case "self":
		return head + "Type /update to install it now (automatic updates are off; /update auto on turns them on)."
	case "readonly":
		return head + "This copy can't update itself; update with:\n" + how.Command
	}
	if how.Auto {
		return head + "It will be installed automatically (" + how.Note + "). To update now:\n" + how.Command
	}
	s := head + "Update with:\n" + how.Command
	if how.Note != "" {
		s += "\nTip: " + how.Note + "."
	}
	return s
}

// rememberedUpdate shows, without going online, an update found by an earlier check that hasn't
// been installed yet.
func (m *model) rememberedUpdate() string {
	if os.Getenv("WERKZ_NO_UPDATE") == "1" || !update.Newer("999.0.0", m.version) ||
		m.cfg.UpdateLatest == "" || !update.Newer(m.cfg.UpdateLatest, m.version) {
		return ""
	}
	how := m.updater.Method(m.cfg.UpdateLatest)
	if how.Kind == "self" && !m.cfg.NoAutoUpdate {
		return "" // the background check installs it
	}
	m.available = m.cfg.UpdateLatest
	return m.updateAdvice(m.cfg.UpdateLatest, how)
}
