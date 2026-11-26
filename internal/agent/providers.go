package agent

import (
	"os"

	"github.com/TonmoyTalukder/werkz/internal/chatgpt"
	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/llm"
	"github.com/TonmoyTalukder/werkz/internal/llm/claude"
	"github.com/TonmoyTalukder/werkz/internal/llm/claudecode"
	"github.com/TonmoyTalukder/werkz/internal/llm/openai"
	"github.com/TonmoyTalukder/werkz/internal/llm/responses"
)

// chatgptSession serializes token refreshes for the ChatGPT plan sign-in in this process.
var chatgptSession chatgpt.Session

func newProvider(cfg *config.Config, id string) llm.Provider {
	switch id {
	case "anthropic":
		return claude.New(cfg.Key("anthropic"), "")
	case "chatgpt":
		return NewChatGPT(cfg.BaseURL(id))
	case "claudecode":
		exe, _ := os.Executable()
		return &claudecode.Client{Bin: config.ClaudeCLI(), Exe: exe}
	}
	return &openai.Client{ID: id, BaseURL: cfg.BaseURL(id), Key: cfg.Key(id)}
}

// NewChatGPT returns a client that uses the stored ChatGPT plan sign-in.
func NewChatGPT(baseURL string) *responses.Client {
	return &responses.Client{ID: "chatgpt", BaseURL: baseURL, Token: chatgptSession.Token, Refresh: chatgptSession.ForceRefresh}
}

// provider returns the client for a provider, reusing one client (and its connection pool)
// per provider, key and endpoint. Tests can swap the factory (e.g. to point Claude at a fake
// server).
func (e *Engine) provider(id string) llm.Provider {
	if e.providerFor != nil {
		return e.providerFor(e.cfg, id)
	}
	key := id + "\x00" + e.cfg.Key(id) + "\x00" + e.cfg.BaseURL(id)
	e.clientsMu.Lock()
	defer e.clientsMu.Unlock()
	if p, ok := e.clients[key]; ok {
		return p
	}
	if e.clients == nil {
		e.clients = map[string]llm.Provider{}
	}
	p := newProvider(e.cfg, id)
	e.clients[key] = p
	return p
}
