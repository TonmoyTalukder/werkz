package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// M4: auto and accept-edits modes are not remembered across sessions.
func TestPermissionModeNotPersisted(t *testing.T) {
	for mode, want := range map[string]string{ModeAuto: ModeDefault, ModeAcceptEdits: ModeDefault, ModePlan: ModePlan, ModeDefault: ModeDefault} {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		c := Load()
		c.Mode = mode
		if err := c.Save(); err != nil {
			t.Fatal(err)
		}
		if got := Load().Mode; got != want {
			t.Errorf("saved %s, next session starts in %s, want %s", mode, got, want)
		}
	}
}

// M8: a broken config file is never overwritten; saves are atomic, owner-only, and merge
// spend from another werkz window.
func TestConfigSaveSafety(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	must(t, os.MkdirAll(filepath.Dir(Path()), 0o700))
	broken := []byte(`{"keys": {"anthropic": "sk-ant-real"},` /* typo: trailing comma */ + "\n}")
	must(t, os.WriteFile(Path(), broken, 0o644))
	c := Load()
	if c.LoadErr() == nil {
		t.Fatal("a broken file should report a load error")
	}
	if err := c.Save(); err == nil {
		t.Fatal("Save must refuse to overwrite a file it couldn't read")
	}
	if b, _ := os.ReadFile(Path()); string(b) != string(broken) {
		t.Fatal("the broken file was modified")
	}

	must(t, os.WriteFile(Path(), []byte(`{"keys":{"anthropic":"k"},"anthropic_spent":1}`), 0o644))
	a, b := Load(), Load() // two werkz windows
	a.Spent += 0.5
	must(t, a.Save())
	b.Spent += 0.25
	must(t, b.Save())
	final := Load()
	if got := final.Spent; got < 1.749 || got > 1.751 {
		t.Fatalf("spend from both windows should add up to 1.75, got %v", got)
	}
	if final.Keys["anthropic"] != "k" {
		t.Fatal("keys lost")
	}
	if st, _ := os.Stat(Path()); st.Mode().Perm() != 0o600 {
		t.Fatalf("config file mode = %v, want 0600", st.Mode().Perm())
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// M12: free providers come first by default; the previous default is migrated, a custom
// order is kept.
func TestDefaultFallbackOrder(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if got := Load().FallbackOrder; !slices.Equal(got, DefaultFallbackOrder) || got[0] != "gemini" {
		t.Fatalf("default order = %v", got)
	}
	c := Load()
	c.FallbackOrder = slices.Clone(oldDefaultOrder)
	must(t, c.Save())
	if got := Load().FallbackOrder; !slices.Equal(got, DefaultFallbackOrder) {
		t.Fatalf("old default not migrated: %v", got)
	}
	c = Load()
	c.FallbackOrder = []string{"openai", "ollama"}
	must(t, c.Save())
	if got := Load().FallbackOrder; !slices.Equal(got, []string{"openai", "ollama"}) {
		t.Fatalf("custom order changed: %v", got)
	}
}

// L4: a model chosen with --model is used for the session but not saved, unless the user
// then picks another model.
func TestSessionModelNotSaved(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c := Load()
	c.Provider, c.Model, c.Effort = "anthropic", "claude-opus-5-5", "medium"
	must(t, c.Save())

	c = Load()
	c.UseSessionModel("ollama", "llama3.2", "auto")
	if c.Provider != "ollama" || c.Model != "llama3.2" {
		t.Fatal("session model not applied")
	}
	must(t, c.Save())
	if d := Load(); d.Provider != "anthropic" || d.Model != "claude-opus-5-5" || d.Effort != "medium" {
		t.Fatalf("--model was saved: %s/%s/%s", d.Provider, d.Model, d.Effort)
	}
	c.Provider, c.Model = "anthropic", "claude-sonnet-5-5" // the user picks a model with /model
	must(t, c.Save())
	if d := Load(); d.Model != "claude-sonnet-5-5" {
		t.Fatalf("a model picked in werkz should be saved, got %s", d.Model)
	}
}

// L5: OLLAMA_HOST works with or without a scheme (and with a trailing /v1).
func TestOllamaHost(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for host, want := range map[string]string{
		"":                          "http://localhost:11434/v1",
		"0.0.0.0:11434":             "http://0.0.0.0:11434/v1",
		"localhost:11434":           "http://localhost:11434/v1",
		"http://gpu-box:11434/":     "http://gpu-box:11434/v1",
		"https://ollama.example/v1": "https://ollama.example/v1",
	} {
		t.Setenv("OLLAMA_HOST", host)
		if got := Load().BaseURL("ollama"); got != want {
			t.Errorf("OLLAMA_HOST=%q → %q, want %q", host, got, want)
		}
	}
}

// Apps started from a desktop icon may not have ~/.local/bin on PATH: Claude Code is still found.
func TestClaudeFoundOutsidePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("WERKZ_CLAUDE_BIN", "")
	if p := findClaude(); p != "" {
		t.Fatalf("nothing installed, found %q", p)
	}
	bin := filepath.Join(home, ".local", "bin", "claude")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if p := findClaude(); p != bin {
		t.Fatalf("found %q, want %q", p, bin)
	}
}

func TestContextWindowLimit(t *testing.T) {
	for in, want := range map[string]int{"350k": 350_000, "1m": 1_000_000, "1.5M": 1_500_000, "200000": 200_000} {
		if n, err := ParseTokens(in); err != nil || n != want {
			t.Errorf("ParseTokens(%q) = %d, %v", in, n, err)
		}
	}
	if _, err := ParseTokens("lots"); err == nil {
		t.Error("expected an error")
	}
	c := &Config{}
	if c.Window(1_000_000) != 1_000_000 {
		t.Error("no limit: the model's full window")
	}
	c.ContextWindow = 350_000
	if c.Window(1_000_000) != 350_000 || c.Window(128_000) != 128_000 {
		t.Error("a limit applies only when it is smaller than the model's window")
	}
}
