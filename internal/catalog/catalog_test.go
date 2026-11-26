package catalog

import "testing"

func TestPricing(t *testing.T) {
	if m := Lookup("anthropic", "claude-opus-4-8"); m.In != 5 || m.Out != 25 || !m.Priced() {
		t.Fatalf("older Claude model should be priced: %+v", m)
	}
	if m := Lookup("anthropic", "claude-unknown-9"); m.Priced() {
		t.Fatal("an unknown Claude ID must not look priced")
	}
	if m := Lookup("openai", "gpt-5"); m.Priced() {
		t.Fatal("OpenAI prices are unknown to werkz")
	}
	// Opus 5.5: reads 0.05x, 5-minute writes 1.25x, output $20/M
	m := Lookup("anthropic", "claude-opus-5-5")
	want := (100*4.0 + 1000*4*0.05 + 200*4*1.25 + 50*20.0) / 1e6
	if got := m.Cost(100, 50, 1000, 200, false, false); got != want {
		t.Fatalf("cost = %v, want %v", got, want)
	}
}

// M10: only genuinely free models are labelled "free".
func TestFreeLabels(t *testing.T) {
	for _, c := range []struct{ provider, id, want string }{
		{"openrouter", "meta-llama/llama-3.3-70b-instruct:free", "free"},
		{"openrouter", "anthropic/claude-sonnet-4.5", ""},
		{"gemini", "gemini-flash-latest", "free tier"},
		{"groq", "llama-3.3-70b-versatile", "free tier"},
		{"ollama", "llama3.2", "local"},
		{"anthropic", "claude-haiku-5-5", ""},
	} {
		if got := Lookup(c.provider, c.id).FreeKind(); got != c.want {
			t.Errorf("%s/%s = %q, want %q", c.provider, c.id, got, c.want)
		}
	}
	if label, paid := PriceLabel(Lookup("gemini", "gemini-flash-latest")); paid || label == "free" {
		t.Fatalf("Gemini should be labelled as a free tier, got %q", label)
	}
}

// L4: OpenAI o-series models are recognised.
func TestGuessProvider(t *testing.T) {
	for id, want := range map[string]string{
		"claude-opus-5-5": "anthropic", "gpt-5": "openai", "o1": "openai", "o3": "openai", "o4-mini": "openai",
		"grok-4": "xai", "gemini-flash-latest": "gemini", "meta-llama/llama-3.3-70b-instruct": "openrouter",
		"llama3.2": "ollama", "openchat": "ollama",
	} {
		if got := GuessProvider(id); got != want {
			t.Errorf("GuessProvider(%q) = %q, want %q", id, got, want)
		}
	}
}
