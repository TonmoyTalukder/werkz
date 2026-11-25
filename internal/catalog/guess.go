package catalog

func GuessProvider(model string) string {
	switch {
	case len(model) > 7 && model[:7] == "claude-":
		return "anthropic"
	case len(model) > 5 && model[:5] == "grok-":
		return "xai"
	case len(model) > 7 && model[:7] == "gemini-":
		return "gemini"
	case len(model) > 4 && (model[:4] == "gpt-" || model[:2] == "o3" || model[:2] == "o4"):
		return "openai"
	}
	for i := range model {
		if model[i] == '/' {
			return "openrouter"
		}
	}
	return "ollama"
}
