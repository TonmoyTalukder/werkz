package tui

import (
	"context"
	"fmt"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/catalog"

	tea "github.com/charmbracelet/bubbletea"
)

// ---------------------------------------------------------------- compaction & idle rule

func (m *model) startCompact(focus string) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.turn.cancel = cancel
	m.turn.active = true
	m.turn.started = time.Now()
	m.turn.approxOut = 0
	m.turn.verb = "Compacting conversation"
	m.turn.live.Reset()
	m.turn.thinking.Reset()
	go m.eng.Compact(ctx, focus, *m.cfg)
	return tick()
}

// idleCheck: after the prompt cache has expired, sending into a big context re-writes all of it
// at the cache-write price. Warn once and offer /compact; pressing Enter again sends anyway.
func (m *model) idleCheck(text string) string {
	const idleMin = 150_000
	if m.cfg.Provider != "anthropic" || m.stats.ctxUsed < idleMin || m.stats.warmUntil.IsZero() || time.Now().Before(m.stats.warmUntil) {
		m.stats.idleWarned = ""
		return ""
	}
	if m.stats.idleWarned == text {
		m.stats.idleWarned = ""
		return ""
	}
	m.stats.idleWarned = text
	mi := m.curModelInfo()
	resend := float64(m.stats.ctxUsed) * mi.In * 1.25 / 1e6
	compact := float64(m.stats.ctxUsed)*mi.In/1e6 + 0.02
	idle := time.Since(m.stats.warmUntil.Add(-5 * time.Minute)).Round(time.Minute)
	return fmt.Sprintf("The prompt cache expired (idle ~%s). Sending now re-writes ~%s tokens (~$%.2f) and every later request carries them. "+
		"/compact first costs ~$%.2f and leaves a short summary. Press Enter again to send anyway.",
		idle, catalog.HumanTokens(m.stats.ctxUsed), resend, compact)
}
