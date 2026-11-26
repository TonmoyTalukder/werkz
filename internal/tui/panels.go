package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/catalog"
	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/textutil"

	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------------- text panels

func (m *model) banner(w int) string {
	wd, _ := os.Getwd()
	if h, err := os.UserHomeDir(); err == nil && strings.HasPrefix(wd, h) {
		wd = "~" + strings.TrimPrefix(wd, h)
	}
	if r := []rune(wd); len(r) > max(w-4, 12) {
		wd = "…" + string(r[len(r)-max(w-5, 11):])
	}
	mi := m.curModelInfo()
	var fb []string
	for _, id := range m.cfg.FallbackOrder {
		if id != m.cfg.Provider && m.cfg.Configured(id) {
			fb = append(fb, id)
		}
	}
	fbs := "none"
	if len(fb) > 0 {
		fbs = strings.Join(fb, " → ")
	}
	billing := "API key"
	switch {
	case m.cfg.Provider == "ollama":
		billing = "local"
	case config.ProviderByID(m.cfg.Provider).Plan():
		billing = "subscription"
	}
	info := strings.Join([]string{
		sBold.Render("werkz") + sDim.Render(" v"+m.version),
		sSky.Render(mi.Label) + sDim.Render(" · "+m.cfg.Provider+" "+billing+" · effort "+m.cfg.Effort),
		sDim.Render("fallback: ") + sText.Render(fbs),
		m.wsBannerLine(wd),
	}, "\n")
	logoW := lipgloss.Width(logo)
	var top string
	switch {
	case w >= logoW+3+lipgloss.Width(info):
		top = lipgloss.JoinHorizontal(lipgloss.Center, gradientBlock(logo, brandStops...), "   ", info)
	case w >= logoW+1:
		top = gradientBlock(logo, brandStops...) + "\n\n" + info
	default:
		top = gradient(smallLogo, 0) + "\n" + info
	}
	tips := sDim.Render("Tips: ") + sText.Render("/model") + sDim.Render(" switch models · ") + sText.Render("/effort") +
		sDim.Render(" thinking level · ") + sText.Render("/login") + sDim.Render(" keys · ") + sText.Render("shift+tab") +
		sDim.Render(" permission mode")
	return "\n" + top + "\n\n" + wrapTo(tips, w) + "\n"
}

func (m *model) helpText() string {
	var b strings.Builder
	b.WriteString(gradient("Commands", 0) + "\n")
	for _, c := range commands {
		b.WriteString("  " + sPink.Render(padRight(c.Name, 12)) + sDim.Render(c.Desc) + "\n")
	}
	b.WriteString("\n" + gradient("Keys", 0) + "\n")
	for _, k := range [][2]string{{"enter", "send (queued while a turn runs)"}, {"ctrl+j / alt+enter", "newline"},
		{"esc", "interrupt the model / close dialog"}, {"shift+tab", "cycle: default → accept edits → plan → auto"},
		{"tab", "complete a slash command"}, {"ctrl+c ×2", "quit"}} {
		b.WriteString("  " + sSky.Render(padRight(k[0], 20)) + sDim.Render(k[1]) + "\n")
	}
	return box(strings.TrimRight(b.String(), "\n"), cViolet, m.w)
}

func (m *model) fallbackText() string {
	var b strings.Builder
	if len(m.cfg.FallbackChain) > 0 {
		b.WriteString(gradient("Fallback chain", 0) + sDim.Render("  (current model first, then the models you chose)") + "\n")
		cur := m.curModelInfo()
		b.WriteString(fmt.Sprintf("  %s %s %s\n", sDim.Render("0."), padRight(providerBadge(cur.Provider), 14), sText.Render(cur.Label)+sDim.Render("  (current)")))
		for i, f := range m.cfg.FallbackChain {
			mi := catalog.Lookup(f.Provider, f.Model)
			status := sGreen.Render("✓ ready")
			if !m.cfg.Configured(f.Provider) {
				status = sDim.Render("✗ no key")
			} else if r := m.eng.Disabled(f.Provider); r != "" {
				status = sRed.Render("✗ " + r)
			}
			price, _ := catalog.PriceLabel(mi)
			b.WriteString(fmt.Sprintf("  %s %s %s %s %s\n", sDim.Render(fmt.Sprintf("%d.", i+1)), padRight(providerBadge(f.Provider), 14),
				padRight(sText.Render(mi.Label), 40), padRight(sDim.Render(textutil.Truncate(price, 14)), 16), status))
		}
		b.WriteString("  " + sDim.Render("mode: ") + m.fallbackModeLabel() + "\n")
		b.WriteString(sDim.Render("  /fallback to change mode and models · /fallback models to edit the list"))
		return box(b.String(), cSky, m.w)
	}
	b.WriteString(gradient("Provider chain", 0) + sDim.Render("  (current provider first, then this order)") + "\n")
	order := append([]string{m.cfg.Provider}, m.cfg.FallbackOrder...)
	seen := map[string]bool{}
	n := 0
	for _, id := range order {
		if seen[id] {
			continue
		}
		seen[id] = true
		status := sGreen.Render("✓ ready")
		if !m.cfg.Configured(id) {
			status = sDim.Render("✗ no key")
		} else if r := m.eng.Disabled(id); r != "" {
			status = sRed.Render("✗ " + r)
		}
		n++
		b.WriteString(fmt.Sprintf("  %s %s %s %s\n", sDim.Render(fmt.Sprintf("%d.", n)), padRight(providerBadge(id), 14),
			padRight(sText.Render(m.cfg.ModelFor(id)), 44), status))
	}
	b.WriteString("  " + sDim.Render("mode: ") + m.fallbackModeLabel() + "\n")
	b.WriteString(sDim.Render("  /fallback ask|auto|off · order: /fallback gemini,openrouter,groq,ollama"))
	return box(b.String(), cSky, m.w)
}

func (m *model) contextText() string {
	mi := m.curModelInfo()
	ctxMax := m.stats.ctxMax
	if ctxMax == 0 {
		ctxMax = mi.Context
	}
	body := gradient("Context window", 0) + "\n" + ctxBar(m.stats.ctxUsed, ctxMax) + "\n" +
		sDim.Render(fmt.Sprintf("%s of %s tokens used by the last request · %d messages in history",
			catalog.HumanTokens(m.stats.ctxUsed), catalog.HumanTokens(ctxMax), m.eng.HistoryLen())) + "\n" + sDim.Render("/clear starts fresh")
	return box(body, cSky, m.w)
}

func (m *model) costText() string {
	var b strings.Builder
	b.WriteString(gradient("Usage & balance", 0) + "\n")
	b.WriteString(fmt.Sprintf("  %s %s\n", sDim.Render(padRight("session cost", 18)), sGreen.Render(fmt.Sprintf("$%.4f", m.stats.cost))))
	if m.stats.unpriced {
		b.WriteString("  " + sDim.Render(padRight("", 18)) + sYellow.Render("+ unknown: some requests used models whose price werkz doesn't know (e.g. OpenAI, xAI); check that provider's billing page") + "\n")
	}
	b.WriteString(fmt.Sprintf("  %s %s in · %s out\n", sDim.Render(padRight("session tokens", 18)), catalog.HumanTokens(int(m.stats.tokIn)), catalog.HumanTokens(int(m.stats.tokOut))))
	if m.cfg.Balance > 0 {
		b.WriteString(fmt.Sprintf("  %s $%.2f − $%.4f spent = %s\n", sDim.Render(padRight("Claude credit", 18)), m.cfg.Balance, m.cfg.Spent,
			sGreen.Bold(true).Render(fmt.Sprintf("~$%.2f left", m.cfg.Balance-m.cfg.Spent))))
	} else {
		b.WriteString("  " + sDim.Render(padRight("Claude credit", 18)) + sDim.Render("not set · /balance 100 to track your Console credit") + "\n")
	}
	if m.stats.orCreditsOK {
		b.WriteString(fmt.Sprintf("  %s $%.2f\n", sDim.Render(padRight("OpenRouter credit", 18)), m.stats.orCredits))
	}
	if m.stats.cacheRead+m.stats.cacheWrite > 0 {
		b.WriteString(fmt.Sprintf("  %s %d%% hit · saved ~$%.4f  (/cache for details)\n", sDim.Render(padRight("prompt cache", 18)), max(m.cacheHitRate(), 0), m.stats.cacheSaved))
	}
	b.WriteString(sFaint.Render("  Claude cost is estimated from token usage; the Console's Billing page is authoritative."))
	return box(b.String(), cGreen, m.w)
}

// ---------------------------------------------------------------- prompt cache info

// cacheHitRate is the share of input tokens served from the cache this session (-1 = no data).
func (m *model) cacheHitRate() int {
	total := m.stats.cacheRead + m.stats.cacheWrite + m.stats.uncached
	if total == 0 || m.stats.cacheRead+m.stats.cacheWrite == 0 {
		return -1
	}
	return int(float64(m.stats.cacheRead) / float64(total) * 100)
}

// cacheWarn explains the cost of a change that invalidates the cached conversation.
func (m *model) cacheWarn(what string) string {
	if m.cfg.Provider != "anthropic" || m.stats.ctxUsed < 10_000 || m.eng.HistoryLen() == 0 {
		return ""
	}
	mi := m.curModelInfo()
	cost := float64(m.stats.ctxUsed) * mi.In * 1.25 / 1e6
	return fmt.Sprintf("%s invalidates the prompt cache: the next request re-writes ~%s tokens (~$%.2f) instead of reading them. /clear first if the conversation isn't needed.",
		what, catalog.HumanTokens(m.stats.ctxUsed), cost)
}

func (m *model) cacheText() string {
	var b strings.Builder
	b.WriteString(gradient("Prompt cache", 0) + "\n")
	row := func(k, v string) { b.WriteString("  " + sDim.Render(padRight(k, 18)) + v + "\n") }
	hr := m.cacheHitRate()
	if hr < 0 {
		row("this session", sDim.Render("no Claude requests yet"))
	} else {
		row("hit rate", sGreen.Bold(true).Render(fmt.Sprintf("%d%%", hr))+sDim.Render(" of input tokens read from cache"))
		row("tokens", fmt.Sprintf("%s read · %s written · %s uncached", catalog.HumanTokens(int(m.stats.cacheRead)), catalog.HumanTokens(int(m.stats.cacheWrite)), catalog.HumanTokens(int(m.stats.uncached))))
		row("saved", sGreen.Render(fmt.Sprintf("~$%.4f", m.stats.cacheSaved))+sDim.Render(" vs paying full input price"))
	}
	if !m.stats.warmUntil.IsZero() && time.Now().Before(m.stats.warmUntil) {
		row("status", sGreen.Render(fmt.Sprintf("warm for ~%s", time.Until(m.stats.warmUntil).Round(time.Second))))
	} else if !m.stats.warmUntil.IsZero() {
		row("status", sYellow.Render("expired · the next request re-writes the conversation"))
	}
	ttl := "5 minutes (refreshed by every request)"
	if m.cfg.TTL1h() {
		ttl = "1 hour (writes cost 2× instead of 1.25×)"
	}
	row("TTL", ttl)
	if w := m.cfg.KeepAliveWindow(); w > 0 && !m.cfg.TTL1h() {
		row("keep-alive", fmt.Sprintf("on · keeps an idle cache warm for %d min", int(w.Minutes()))+
			sDim.Render(fmt.Sprintf(" · %d pings ($%.4f)", m.stats.kaPings, m.stats.kaCost)))
	} else {
		row("keep-alive", sDim.Render("off"))
	}
	b.WriteString(sFaint.Render("  /cache ttl 5m|1h · /cache keepalive on|off|<minutes> · switching model/effort/fast mode invalidates the cache"))
	return box(b.String(), cGreen, m.w)
}
