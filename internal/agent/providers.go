package agent

import (
	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/llm"
	"github.com/TonmoyTalukder/werkz/internal/llm/claude"
	"github.com/TonmoyTalukder/werkz/internal/llm/openai"
)

func newProvider(cfg *config.Config, id string) llm.Provider {
	if id == "anthropic" {
		return claude.New(cfg.Key("anthropic"), "")
	}
	return &openai.Client{ID: id, BaseURL: cfg.BaseURL(id), Key: cfg.Key(id)}
}

// provider returns the client for a provider; tests can swap the factory (e.g. to point
// Claude at a fake server).
func (e *Engine) provider(id string) llm.Provider {
	if e.providerFor != nil {
		return e.providerFor(e.cfg, id)
	}
	return newProvider(e.cfg, id)
}
