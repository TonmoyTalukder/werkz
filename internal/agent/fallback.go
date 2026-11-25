package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/TonmoyTalukder/werkz/internal/config"
)

type attempt struct{ provider, model string }

// chain is the main model first (always, even if it is unusable this session, so the
// fallback mode decides what happens next), then the usable fallback models.
func (e *Engine) chain() []attempt {
	var out []attempt
	seen := map[string]bool{}
	if m := e.cfg.ModelFor(e.cfg.Provider); m != "" {
		main := attempt{e.cfg.Provider, m}
		seen[main.key()] = true
		out = append(out, main)
	}
	add := func(p, model string) {
		key := p + "|" + model
		if model == "" || seen[key] || !e.cfg.Configured(p) || e.disabled[p] != "" {
			return
		}
		seen[key] = true
		out = append(out, attempt{p, model})
	}
	if len(e.cfg.FallbackChain) > 0 {
		// the exact models the user chose, in their order
		for _, f := range e.cfg.FallbackChain {
			add(f.Provider, f.Model)
		}
		return out
	}
	for _, p := range e.cfg.FallbackOrder {
		add(p, e.cfg.ModelFor(p))
	}
	return out
}

func (a attempt) key() string { return a.provider + "|" + a.model }

// unusable explains why a provider can't be used right now ("" = usable).
func (e *Engine) unusable(provider string) string {
	if r := e.Disabled(provider); r != "" {
		return r + " (earlier this session)"
	}
	if !e.cfg.Configured(provider) {
		return "not connected (add a key with /login)"
	}
	return ""
}

// nextFallback decides which model to try after a failure, following the fallback mode:
// off stops; auto takes the next untried model in the chain; ask lets the user pick any
// connected model (the chain is suggested first) or stop.
func (e *Engine) nextFallback(ctx context.Context, from attempt, chain []attempt, tried map[string]bool, lastErr error) (attempt, error) {
	var remaining []attempt
	for _, c := range chain[1:] {
		if !tried[c.key()] && e.disabled[c.provider] == "" {
			remaining = append(remaining, c)
		}
	}
	switch e.cfg.Fallback() {
	case config.FallbackOff:
		return attempt{}, fmt.Errorf("%w · fallback is off (choose a fallback mode with /fallback)", lastErr)
	case config.FallbackAuto:
		if len(remaining) == 0 {
			if len(chain) == 1 {
				return attempt{}, fmt.Errorf("%w · no fallback models available (/fallback models to choose some)", lastErr)
			}
			return attempt{}, fmt.Errorf("all fallback models failed (last: %w)", lastErr)
		}
		return remaining[0], nil
	}
	// ask
	for _, c := range remaining {
		if e.fbSession[c.key()] || e.fbTurn[c.key()] {
			return c, nil // already approved for this message or session
		}
	}
	var cands, done []config.FallbackModel
	for _, c := range remaining {
		cands = append(cands, config.FallbackModel{Provider: c.provider, Model: c.model})
	}
	for k := range tried {
		p := strings.SplitN(k, "|", 2)
		done = append(done, config.FallbackModel{Provider: p[0], Model: p[1]})
	}
	for p, r := range e.disabled {
		if r != "" {
			done = append(done, config.FallbackModel{Provider: p}) // whole provider unusable this session
		}
	}
	reply := make(chan FallbackChoice, 1)
	e.send(FallbackAskMsg{From: from.provider, FromModel: from.model,
		Reason: strings.TrimPrefix(lastErr.Error(), from.provider+": "), Candidates: cands, Exclude: done, Reply: reply})
	select {
	case ch := <-reply:
		if ch.Stop || ch.Model == "" {
			return attempt{}, fmt.Errorf("%w · fallback declined, pick another model with /model", lastErr)
		}
		next := attempt{ch.Provider, ch.Model}
		if ch.Session {
			e.fbSession[next.key()] = true
		} else {
			e.fbTurn[next.key()] = true
		}
		return next, nil
	case <-ctx.Done():
		return attempt{}, context.Canceled
	}
}
