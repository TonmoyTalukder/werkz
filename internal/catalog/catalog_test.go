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
