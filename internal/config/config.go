// Package config holds werkz settings and the provider registry, persisted to
// ~/.config/werkz/config.json with owner-only permissions.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/chatgpt"
)

// ProviderInfo describes one backend werkz can talk to.
type ProviderInfo struct {
	ID           string
	Name         string
	EnvVar       string
	BaseURL      string // OpenAI-compatible base URL (empty for Anthropic)
	DefaultModel string
	KeyHint      string
	SignupURL    string
	NeedsKey     bool
	Free         bool // has a usable free tier
	// Auth is how a provider without an API key is set up: AuthChatGPT (browser sign-in) or
	// AuthCLI (an installed command-line tool that is already signed in).
	Auth string
}

// Provider set-up kinds (ProviderInfo.Auth); "" means an API key.
const (
	AuthChatGPT = "chatgpt"
	AuthCLI     = "cli"
)

// Plan reports whether requests use the user's subscription (no per-token charge).
func (p *ProviderInfo) Plan() bool { return p != nil && p.Auth != "" }

var Providers = []ProviderInfo{
	{ID: "anthropic", Name: "Claude (Anthropic)", EnvVar: "ANTHROPIC_API_KEY", DefaultModel: "claude-opus-5-5",
		KeyHint: "sk-ant-…", SignupURL: "platform.claude.com/settings/keys", NeedsKey: true},
	{ID: "openai", Name: "OpenAI / Codex", EnvVar: "OPENAI_API_KEY", BaseURL: "https://api.openai.com/v1",
		DefaultModel: "gpt-5", KeyHint: "sk-…", SignupURL: "platform.openai.com/api-keys", NeedsKey: true},
	{ID: "xai", Name: "xAI Grok", EnvVar: "XAI_API_KEY", BaseURL: "https://api.x.ai/v1",
		DefaultModel: "grok-4", KeyHint: "xai-…", SignupURL: "console.x.ai", NeedsKey: true},
	{ID: "gemini", Name: "Google Gemini (free tier)", EnvVar: "GEMINI_API_KEY",
		BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", DefaultModel: "gemini-2.5-flash",
		KeyHint: "AIza…", SignupURL: "aistudio.google.com/apikey", NeedsKey: true, Free: true},
	{ID: "openrouter", Name: "OpenRouter", EnvVar: "OPENROUTER_API_KEY", BaseURL: "https://openrouter.ai/api/v1",
		DefaultModel: "baidu/ernie-5.5-flash-preview:free", KeyHint: "sk-or-…", SignupURL: "openrouter.ai/keys",
		NeedsKey: true, Free: true},
	{ID: "groq", Name: "Groq (free tier)", EnvVar: "GROQ_API_KEY", BaseURL: "https://api.groq.com/openai/v1",
		DefaultModel: "llama-3.3-70b-versatile", KeyHint: "gsk_…", SignupURL: "console.groq.com/keys", NeedsKey: true, Free: true},
	{ID: "ollama", Name: "Ollama (local, offline)", EnvVar: "OLLAMA_HOST", BaseURL: "http://localhost:11434/v1",
		DefaultModel: "llama3.2", KeyHint: "http://localhost:11434 (optional)", SignupURL: "ollama.com/download", Free: true},
	{ID: "chatgpt", Name: "ChatGPT plan", BaseURL: "https://api.openai.com/v1", DefaultModel: "gpt-6.2-sol",
		SignupURL: "/login chatgpt", Auth: AuthChatGPT},
	{ID: "claudecode", Name: "Claude Code (your subscription)", DefaultModel: "default",
		SignupURL: "code.claude.com", Auth: AuthCLI},
}

// KeyProviders are the providers set up with an API key (or, for Ollama, a host).
func KeyProviders() []ProviderInfo {
	var out []ProviderInfo
	for _, p := range Providers {
		if p.Auth == "" {
			out = append(out, p)
		}
	}
	return out
}

func ProviderByID(id string) *ProviderInfo {
	for i := range Providers {
		if Providers[i].ID == id {
			return &Providers[i]
		}
	}
	return nil
}

// Config is persisted to ~/.config/werkz/config.json with 0600 permissions.
type Config struct {
	Keys           map[string]string `json:"keys"`
	Provider       string            `json:"provider"`
	Model          string            `json:"model"`
	Effort         string            `json:"effort"`
	Fast           bool              `json:"fast"`
	ProviderModels map[string]string `json:"provider_models"` // model used when a provider is a fallback
	FallbackOrder  []string          `json:"fallback_order"`
	Balance        float64           `json:"anthropic_balance"` // user-entered starting credit
	Spent          float64           `json:"anthropic_spent"`   // estimated spend since balance was set
	Onboarded      bool              `json:"onboarded"`
	Mode           string            `json:"permission_mode"`
	Workspaces     []*Workspace      `json:"workspaces,omitempty"`

	loadErr         error           // set when the file existed but couldn't be loaded: Save refuses
	session         *sessionModel   // a model chosen on the command line: used, but not saved
	loadedSpent     float64         // Spent as last read from or written to disk (merging across windows)
	FallbackMode    string          `json:"fallback_mode"`               // off (default) | ask | auto
	OpenRouterPaid  bool            `json:"openrouter_paid,omitempty"`   // also offer paid OpenRouter models (default: free only)
	NoAutoUpdate    bool            `json:"auto_update_off,omitempty"`   // don\'t install new releases automatically
	NoHistory       bool            `json:"history_off,omitempty"`       // don't save conversations for /resume
	WebSearch       string          `json:"web_search,omitempty"`        // "" (auto) | provider | brave | tavily | searxng | off
	SearXNGURL      string          `json:"searxng_url,omitempty"`       // a SearXNG instance for WebSearch
	UpdateCheckedAt time.Time       `json:"update_checked_at,omitempty"` // last time werkz looked for a new release
	UpdateLatest    string          `json:"update_latest,omitempty"`     // newest release seen by that check
	CacheTTL        string          `json:"cache_ttl,omitempty"`         // 5m (default) | 1h
	KeepAliveMin    int             `json:"cache_keepalive_minutes"`     // 0 = default (30)
	NoKeepAlive     bool            `json:"cache_keepalive_off,omitempty"`
	FallbackChain   []FallbackModel `json:"fallback_models,omitempty"`     // ordered; empty = one model per provider in FallbackOrder
	CompactAt       int             `json:"auto_compact_tokens,omitempty"` // 0 = default (300k)
	NoAutoCompact   bool            `json:"auto_compact_off,omitempty"`
	PlanNoticeSeen  bool            `json:"chatgpt_plan_notice_seen,omitempty"` // "You're using your ChatGPT plan" shown once
	ContextWindow   int             `json:"context_window,omitempty"`           // user's limit in tokens; 0 = each model's full window
}

// Window is the context window werkz works with for a model whose full window is modelMax: the
// user's limit when it is smaller.
func (c *Config) Window(modelMax int) int {
	if c.ContextWindow > 0 && (modelMax <= 0 || c.ContextWindow < modelMax) {
		return c.ContextWindow
	}
	return modelMax
}

// ParseTokens reads a token count such as "350k", "1m", "1.5M" or "350000".
func ParseTokens(s string) (int, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	mult := 1.0
	switch {
	case strings.HasSuffix(s, "k"):
		mult, s = 1e3, strings.TrimSuffix(s, "k")
	case strings.HasSuffix(s, "m"):
		mult, s = 1e6, strings.TrimSuffix(s, "m")
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return 0, fmt.Errorf("not a token count: %q (try 350k or 1m)", s)
	}
	return int(f * mult), nil
}

// Permission modes (Config.Mode).
const (
	ModeDefault     = "default"     // edits and commands ask
	ModeAcceptEdits = "acceptEdits" // edits allowed, commands ask
	ModePlan        = "plan"        // read-only
	ModeAuto        = "auto"        // no prompts (writes outside the working folders still ask)
	// ModeBypass asks for nothing, not even outside the working folders (credential locations
	// stay blocked). It is turned on only after a confirmation and is never saved.
	ModeBypass = "bypass"
)

// DefaultFallbackOrder tries free and local providers before paid ones.
// Subscriptions come next: they don't cost extra per request.
var DefaultFallbackOrder = []string{"gemini", "openrouter", "groq", "ollama", "chatgpt", "claudecode", "anthropic", "openai", "xai"}

// Earlier defaults, migrated to the current one (a custom order is kept).
var (
	oldDefaultOrder  = []string{"anthropic", "openai", "xai", "gemini", "openrouter", "groq", "ollama"}
	prevDefaultOrder = []string{"gemini", "openrouter", "groq", "ollama", "anthropic", "openai", "xai"}
)

// Fallback modes (Config.FallbackMode).
const (
	FallbackOff  = "off"
	FallbackAsk  = "ask"
	FallbackAuto = "auto"
)

// FallbackModel is one entry of the user's ordered fallback chain.
type FallbackModel struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

func (c *Config) TTL1h() bool { return c.CacheTTL == "1h" }

// KeepAliveWindow is how long an idle Claude cache is kept warm (0 = off).
func (c *Config) KeepAliveWindow() time.Duration {
	if c.NoKeepAlive {
		return 0
	}
	if c.KeepAliveMin <= 0 {
		return 30 * time.Minute
	}
	return time.Duration(c.KeepAliveMin) * time.Minute
}

// Fallback returns how provider fallback behaves: off (default), ask or auto.
func (c *Config) Fallback() string {
	switch c.FallbackMode {
	case FallbackAsk, FallbackAuto:
		return c.FallbackMode
	}
	return FallbackOff
}

// Workspace is an optional named set of folders (first = main) plus env vars for shell commands.
type Workspace struct {
	Name     string            `json:"name"`
	Dirs     []string          `json:"dirs"`
	Env      map[string]string `json:"env,omitempty"`
	Starred  bool              `json:"starred,omitempty"`
	LastUsed time.Time         `json:"last_used,omitempty"`
}

func (c *Config) Workspace(name string) *Workspace {
	for _, w := range c.Workspaces {
		if strings.EqualFold(w.Name, name) {
			return w
		}
	}
	return nil
}

func Path() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "werkz", "config.json")
}

// Load reads the config file. If the file exists but can't be read or parsed, Load returns
// defaults and remembers the error (see LoadErr): Save then refuses to overwrite the file, so a
// typo made while hand-editing it never wipes the user's keys and workspaces.
func Load() *Config {
	c := &Config{}
	b, err := os.ReadFile(Path())
	switch {
	case err == nil:
		if jerr := json.Unmarshal(b, c); jerr != nil {
			*c = Config{}
			c.loadErr = fmt.Errorf("%s is not valid JSON (%w); fix or remove it", Path(), jerr)
		}
	case !errors.Is(err, fs.ErrNotExist):
		c.loadErr = fmt.Errorf("can't read %s: %w", Path(), err)
	}
	c.loadedSpent = c.Spent
	if c.Keys == nil {
		c.Keys = map[string]string{}
	}
	if c.ProviderModels == nil {
		c.ProviderModels = map[string]string{}
	}
	// Free options first: a failure (most often a free-tier rate limit) shouldn't escalate
	// straight to the most expensive models. The old default is migrated; a custom order stays.
	if len(c.FallbackOrder) == 0 || slices.Equal(c.FallbackOrder, oldDefaultOrder) || slices.Equal(c.FallbackOrder, prevDefaultOrder) {
		c.FallbackOrder = slices.Clone(DefaultFallbackOrder)
	}
	// Prompt-free modes are per session: a new session (maybe in a freshly cloned, untrusted
	// repo) always starts asking. Only the stricter plan mode is remembered.
	if c.Mode != ModePlan {
		c.Mode = ModeDefault
	}
	return c
}

type sessionModel struct {
	provider, model           string // what --model/--provider selected
	savedProvider, savedModel string // what the config had before
	savedEffort               string
}

// UseSessionModel switches to a model for this session only (command-line flags): it is used
// like any other choice, but Save keeps the user's saved model unless they pick another one.
func (c *Config) UseSessionModel(provider, model, effort string) {
	c.session = &sessionModel{provider: provider, model: model,
		savedProvider: c.Provider, savedModel: c.Model, savedEffort: c.Effort}
	c.Provider, c.Model, c.Effort = provider, model, effort
}

// LoadErr reports why the config file couldn't be loaded (nil when it loaded or didn't exist).
func (c *Config) LoadErr() error { return c.loadErr }

// Save writes the config atomically (temp file, fsync, rename) with owner-only permissions.
// Spend recorded by another werkz window since this one loaded is merged in, not overwritten.
func (c *Config) Save() error {
	if c.loadErr != nil {
		return fmt.Errorf("not saving settings: %w", c.loadErr)
	}
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	if b, err := os.ReadFile(p); err == nil {
		var disk Config
		if json.Unmarshal(b, &disk) == nil && disk.Spent != c.loadedSpent && disk.Balance == c.Balance {
			c.Spent += disk.Spent - c.loadedSpent // another window spent money meanwhile
		}
	}
	out := c
	if s := c.session; s != nil && c.Provider == s.provider && c.Model == s.model {
		cp := *c // still on the command-line model: save what the user had chosen before
		cp.Provider, cp.Model, cp.Effort = s.savedProvider, s.savedModel, s.savedEffort
		out = &cp
	}
	if out.Mode == ModeBypass {
		cp := *out // bypass is never written to disk: werkz always reopens asking
		cp.Mode = ModeDefault
		out = &cp
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".config-*.json") // created with mode 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return err
	}
	c.loadedSpent = c.Spent
	return nil
}

// Key returns the stored key, falling back to the provider's environment variable.
func (c *Config) Key(id string) string {
	if k := strings.TrimSpace(c.Keys[id]); k != "" {
		return k
	}
	if p := ProviderByID(id); p != nil {
		return strings.TrimSpace(os.Getenv(p.EnvVar))
	}
	return ""
}

// SearchKey returns the key of a web search service (brave, tavily): saved, or from its
// environment variable (BRAVE_API_KEY, TAVILY_API_KEY).
func (c *Config) SearchKey(id string) string {
	if k := strings.TrimSpace(c.Keys[id]); k != "" {
		return k
	}
	return strings.TrimSpace(os.Getenv(strings.ToUpper(id) + "_API_KEY"))
}

// SearXNG returns the SearXNG instance for web search ("" = none), also from SEARXNG_URL.
func (c *Config) SearXNG() string {
	if u := strings.TrimSpace(c.SearXNGURL); u != "" {
		return u
	}
	return strings.TrimSpace(os.Getenv("SEARXNG_URL"))
}

// Configured reports whether a provider can be tried at all.
func (c *Config) Configured(id string) bool {
	switch id {
	case "ollama":
		return true // reachability is checked at call time
	case "chatgpt":
		return chatgpt.SignedIn()
	case "claudecode":
		return ClaudeCLI() != ""
	}
	return c.Key(id) != ""
}

var claudeCLI struct {
	sync.Mutex
	at   time.Time
	path string
}

// ClaudeCLI returns the path of the installed Claude Code command ("" if not installed). The
// result is cached for a short while, since this is asked often while drawing the screen.
func ClaudeCLI() string {
	// WERKZ_CLAUDE_BIN picks another `claude`; "off" hides Claude Code (tests use this).
	if v := os.Getenv("WERKZ_CLAUDE_BIN"); v != "" {
		if v == "off" {
			return ""
		}
		return v
	}
	claudeCLI.Lock()
	defer claudeCLI.Unlock()
	if time.Since(claudeCLI.at) > 30*time.Second {
		claudeCLI.path = findClaude()
		claudeCLI.at = time.Now()
	}
	return claudeCLI.path
}

// findClaude looks for `claude` on PATH, then in Claude Code's usual install locations: apps
// started from a desktop icon often run without ~/.local/bin on their PATH.
func findClaude() string {
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{
		filepath.Join(home, ".local", "bin", "claude"),      // native installer
		filepath.Join(home, ".claude", "local", "claude"),   // older local install
		filepath.Join(home, ".npm-global", "bin", "claude"), // npm with a user prefix
		filepath.Join(home, ".bun", "bin", "claude"),
		"/opt/homebrew/bin/claude", "/usr/local/bin/claude", "/home/linuxbrew/.linuxbrew/bin/claude",
	} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

func (c *Config) BaseURL(id string) string {
	p := ProviderByID(id)
	if p == nil {
		return ""
	}
	if id == "ollama" {
		if h := c.Key("ollama"); h != "" {
			if !strings.Contains(h, "://") {
				h = "http://" + h // OLLAMA_HOST is often just host:port, e.g. 0.0.0.0:11434
			}
			return strings.TrimSuffix(strings.TrimRight(h, "/"), "/v1") + "/v1"
		}
	}
	return p.BaseURL
}

func (c *Config) ModelFor(id string) string {
	if id == c.Provider && c.Model != "" {
		return c.Model
	}
	if m := c.ProviderModels[id]; m != "" {
		return m
	}
	if p := ProviderByID(id); p != nil {
		return p.DefaultModel
	}
	return ""
}

// AnyConfigured reports whether at least one remote provider has a key or a sign-in.
func (c *Config) AnyConfigured() bool {
	for _, p := range Providers {
		if p.NeedsKey && c.Key(p.ID) != "" || p.Auth == AuthChatGPT && c.Configured(p.ID) {
			return true
		}
	}
	return false
}
