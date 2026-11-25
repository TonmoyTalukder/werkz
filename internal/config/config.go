// Package config holds werkz settings and the provider registry, persisted to
// ~/.config/werkz/config.json with owner-only permissions.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
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
}

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

	loadErr        error           // set when the file existed but couldn't be loaded: Save refuses
	loadedSpent    float64         // Spent as last read from or written to disk (merging across windows)
	FallbackMode   string          `json:"fallback_mode"`             // off (default) | ask | auto
	OpenRouterPaid bool            `json:"openrouter_paid,omitempty"` // also offer paid OpenRouter models (default: free only)
	CacheTTL       string          `json:"cache_ttl,omitempty"`       // 5m (default) | 1h
	KeepAliveMin   int             `json:"cache_keepalive_minutes"`   // 0 = default (30)
	NoKeepAlive    bool            `json:"cache_keepalive_off,omitempty"`
	FallbackChain  []FallbackModel `json:"fallback_models,omitempty"`     // ordered; empty = one model per provider in FallbackOrder
	CompactAt      int             `json:"auto_compact_tokens,omitempty"` // 0 = default (300k)
	NoAutoCompact  bool            `json:"auto_compact_off,omitempty"`
}

// Permission modes (Config.Mode).
const (
	ModeDefault     = "default"     // edits and commands ask
	ModeAcceptEdits = "acceptEdits" // edits allowed, commands ask
	ModePlan        = "plan"        // read-only
	ModeAuto        = "auto"        // no prompts (writes outside the working folders still ask)
)

// DefaultFallbackOrder tries free and local providers before paid ones.
var DefaultFallbackOrder = []string{"gemini", "openrouter", "groq", "ollama", "anthropic", "openai", "xai"}

// oldDefaultOrder was the default before free providers came first.
var oldDefaultOrder = []string{"anthropic", "openai", "xai", "gemini", "openrouter", "groq", "ollama"}

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
	if len(c.FallbackOrder) == 0 || slices.Equal(c.FallbackOrder, oldDefaultOrder) {
		c.FallbackOrder = slices.Clone(DefaultFallbackOrder)
	}
	// Prompt-free modes are per session: a new session (maybe in a freshly cloned, untrusted
	// repo) always starts asking. Only the stricter plan mode is remembered.
	if c.Mode != ModePlan {
		c.Mode = ModeDefault
	}
	return c
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
	b, err := json.MarshalIndent(c, "", "  ")
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

// Configured reports whether a provider can be tried at all.
func (c *Config) Configured(id string) bool {
	if id == "ollama" {
		return true // reachability is checked at call time
	}
	return c.Key(id) != ""
}

func (c *Config) BaseURL(id string) string {
	p := ProviderByID(id)
	if p == nil {
		return ""
	}
	if id == "ollama" {
		if h := c.Key("ollama"); h != "" {
			return strings.TrimRight(h, "/") + "/v1"
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

// AnyConfigured reports whether at least one remote provider has a key.
func (c *Config) AnyConfigured() bool {
	for _, p := range Providers {
		if p.NeedsKey && c.Key(p.ID) != "" {
			return true
		}
	}
	return false
}
