package catalog

import "fmt"

// FreeKind says how a non-paid model is free: "local" (Ollama), "free" (OpenRouter :free
// variants), or "free tier" (Gemini, Groq: free only while the key's project has no billing).
// It returns "" for paid models.
func (m ModelInfo) FreeKind() string {
	switch {
	case m.Provider == "ollama":
		return "local"
	case !m.Free:
		return ""
	case m.Provider == "openrouter":
		return "free"
	}
	return "free tier"
}

// PriceLabel describes what a model costs and whether it is paid.
func PriceLabel(mi ModelInfo) (string, bool) {
	switch k := mi.FreeKind(); {
	case k == "free tier":
		return "free tier (billed if your key's project has billing on)", false
	case k != "":
		return k, false
	case mi.In > 0:
		return fmt.Sprintf("paid · $%g in / $%g out per 1M tokens", mi.In, mi.Out), true
	}
	return "paid · billed by " + mi.Provider, true
}
