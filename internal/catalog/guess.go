package catalog

import (
	"regexp"
	"strings"
)

// openAIReasoning matches OpenAI's o-series model ids (o1, o3, o4-mini, …).
var openAIReasoning = regexp.MustCompile(`^o\d+(-|$)`)

// GuessProvider infers the provider from a model id. Ids that don't identify their provider
// (e.g. Groq's "llama-3.3-70b-versatile" vs a local Ollama "llama3.2") default to Ollama;
// use --provider to be explicit.
func GuessProvider(model string) string {
	switch {
	case strings.HasPrefix(model, "claude-"):
		return "anthropic"
	case strings.HasPrefix(model, "grok-"):
		return "xai"
	case strings.HasPrefix(model, "gemini-"):
		return "gemini"
	case strings.HasPrefix(model, "gpt-"), strings.HasPrefix(model, "chatgpt-"), strings.HasPrefix(model, "codex-"),
		openAIReasoning.MatchString(model):
		return "openai"
	case strings.Contains(model, "/"):
		return "openrouter"
	}
	return "ollama"
}
