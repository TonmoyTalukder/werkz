// Package catalog describes the models werkz can use: context windows, effort levels
// and pricing (including prompt-cache multipliers).
package catalog

import (
	"fmt"
	"strings"
	"sync"

	"github.com/TonmoyTalukder/werkz/internal/config"
)

// ModelInfo holds what the UI needs to show and price a model.
type ModelInfo struct {
	ID             string
	Label          string
	Provider       string
	Context        int
	In, Out        float64 // USD per 1M tokens (0 = free or unknown)
	FastIn         float64 // >0 means fast mode is supported
	FastOut        float64
	Efforts        []string
	DefaultEffort  string
	ServerFallback bool    // supports Anthropic server-side refusal fallbacks
	CacheReadX     float64 // cache-read price as a fraction of input price (0 = 0.1)
	MidSystem      bool    // accepts role:"system" messages inside messages[]
	Note           string
	Free           bool
}

var ClaudeEfforts = []string{"low", "medium", "high", "xhigh", "max"}

var ClaudeModels = []ModelInfo{
	{ID: "claude-fable-5-2", Label: "Fable 5.2", Provider: "anthropic", Context: 1_000_000, In: 8, Out: 40,
		FastIn: 16, FastOut: 80, Efforts: ClaudeEfforts, DefaultEffort: "high", ServerFallback: true,
		CacheReadX: 0.025, MidSystem: true, Note: "most capable · long, hard tasks"},
	{ID: "claude-opus-5-5", Label: "Opus 5.5", Provider: "anthropic", Context: 1_000_000, In: 4, Out: 20,
		FastIn: 8, FastOut: 40, Efforts: ClaudeEfforts, DefaultEffort: "medium", ServerFallback: true,
		CacheReadX: 0.05, MidSystem: true, Note: "complex projects · agents · coding"},
	{ID: "claude-sonnet-5-5", Label: "Sonnet 5.5", Provider: "anthropic", Context: 1_000_000, In: 2, Out: 10,
		Efforts: ClaudeEfforts, DefaultEffort: "high", ServerFallback: true, CacheReadX: 0.05, MidSystem: true,
		Note: "everyday tasks · cost-efficient"},
	{ID: "claude-haiku-5-5", Label: "Haiku 5.5", Provider: "anthropic", Context: 1_000_000, In: 0.10, Out: 0.50,
		Efforts: ClaudeEfforts, DefaultEffort: "medium", CacheReadX: 0.1, MidSystem: true, Note: "fastest · lowest cost"},
}

var OpenAIEfforts = []string{"auto", "low", "medium", "high"}

// remote model lists fetched from OpenAI-compatible /models endpoints
var (
	remoteMu     sync.Mutex
	remoteModels = map[string][]ModelInfo{}
)

func SetRemote(provider string, ms []ModelInfo) {
	remoteMu.Lock()
	remoteModels[provider] = ms
	remoteMu.Unlock()
}

// Remote returns the last fetched model list for a provider (nil if not fetched).
func Remote(provider string) []ModelInfo {
	remoteMu.Lock()
	defer remoteMu.Unlock()
	return append([]ModelInfo(nil), remoteModels[provider]...)
}

func Lookup(provider, id string) ModelInfo {
	if provider == "anthropic" {
		for _, m := range ClaudeModels {
			if m.ID == id {
				return m
			}
		}
		mi := ModelInfo{ID: id, Label: id, Provider: provider, Context: 1_000_000, Efforts: ClaudeEfforts, DefaultEffort: "high"}
		if p, ok := olderClaudePrices[id]; ok {
			mi.In, mi.Out, mi.CacheReadX = p[0], p[1], p[2]
		}
		return mi
	}
	remoteMu.Lock()
	defer remoteMu.Unlock()
	for _, m := range remoteModels[provider] {
		if m.ID == id {
			return m
		}
	}
	return ModelInfo{ID: id, Label: id, Provider: provider, Context: 128_000, Efforts: OpenAIEfforts,
		DefaultEffort: "auto", Free: IsFree(provider, id)}
}

// IsFree reports whether a model costs nothing to use. On OpenRouter only ":free" variants are
// free (it also sells paid models); the other free-tier providers are free for every model.
func IsFree(provider, id string) bool {
	if provider == "openrouter" {
		return strings.HasSuffix(id, ":free")
	}
	p := config.ProviderByID(provider)
	return p != nil && p.Free
}

// allModels returns everything the picker should show for configured providers.
func All(cfg *config.Config) []ModelInfo {
	var out []ModelInfo
	if cfg.Configured("anthropic") {
		out = append(out, ClaudeModels...)
	}
	remoteMu.Lock()
	defer remoteMu.Unlock()
	for _, p := range config.Providers {
		if p.ID == "anthropic" || !cfg.Configured(p.ID) {
			continue
		}
		if ms := remoteModels[p.ID]; len(ms) > 0 {
			out = append(out, ms...)
		} else {
			out = append(out, ModelInfo{ID: cfg.ModelFor(p.ID), Label: cfg.ModelFor(p.ID), Provider: p.ID,
				Context: 128_000, Efforts: OpenAIEfforts, DefaultEffort: "auto", Free: IsFree(p.ID, cfg.ModelFor(p.ID))})
		}
	}
	return out
}

// Cost prices one request. Cache writes are 1.25x input (5-minute TTL) or 2x (1-hour);
// cache reads use the model's read multiplier (0.025x Fable, 0.05x Opus/Sonnet 5.5, else 0.1x).
func (m ModelInfo) Cost(in, out, cacheRead, cacheWrite int64, fast, ttl1h bool) float64 {
	pin, pout := m.In, m.Out
	if fast && m.FastIn > 0 {
		pin, pout = m.FastIn, m.FastOut
	}
	if m.ID == "claude-haiku-5-5" && in+cacheRead+cacheWrite > 100_000 {
		pin, pout = 0.50, 2.50 // long-prompt tier
	}
	rx := m.CacheReadX
	if rx == 0 {
		rx = 0.1
	}
	wx := 1.25
	if ttl1h {
		wx = 2
	}
	return (float64(in)*pin + float64(cacheRead)*pin*rx + float64(cacheWrite)*pin*wx + float64(out)*pout) / 1e6
}

// CacheSaving is what cache reads saved versus paying full input price.
func (m ModelInfo) CacheSaving(cacheRead int64, fast bool) float64 {
	pin := m.In
	if fast && m.FastIn > 0 {
		pin = m.FastIn
	}
	rx := m.CacheReadX
	if rx == 0 {
		rx = 0.1
	}
	return float64(cacheRead) * pin * (1 - rx) / 1e6
}

func (m ModelInfo) PriceTag() string {
	if k := m.FreeKind(); k != "" {
		return k
	}
	if m.In == 0 {
		return ""
	}
	return fmt.Sprintf("$%g/$%g", m.In, m.Out)
}

func HumanTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%gM", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

// olderClaudePrices prices previous Claude models that can still be used with --model or a
// fallback list: input and output USD per 1M tokens, and the cache-read multiplier.
var olderClaudePrices = map[string][3]float64{
	"claude-fable-5-1":  {10, 50, 0.025},
	"claude-fable-5":    {10, 50, 0.1},
	"claude-opus-5":     {5, 25, 0.1},
	"claude-opus-4-8":   {5, 25, 0.1},
	"claude-opus-4-7":   {5, 25, 0.1},
	"claude-opus-4-6":   {5, 25, 0.1},
	"claude-sonnet-5":   {2, 10, 0.1},
	"claude-sonnet-4-6": {3, 15, 0.1},
	"claude-haiku-4-5":  {1, 5, 0.1},
}

// Priced reports whether werkz can estimate this model's cost (free, or a known price).
func (m ModelInfo) Priced() bool { return m.Free || m.In > 0 || m.Provider == "ollama" }
