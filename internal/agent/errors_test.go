package agent

import (
	"strings"
	"testing"

	"github.com/TonmoyTalukder/werkz/internal/llm"
)

// A 404 keeps the provider's reason: Gemini says when a model is retired and what replaces it.
func TestModelNotFoundKeepsReason(t *testing.T) {
	body := "[{\n  \"error\": {\n    \"code\": 404,\n    \"message\": \"This model models/gemini-2.5-flash is no longer available to new users. Please update your code to use models/gemini-3.8-flash.\",\n    \"status\": \"NOT_FOUND\"\n  }\n}\n]"
	got, sticky := Classify("gemini", &llm.HTTPError{Status: 404, Body: body})
	if sticky || !strings.Contains(got, "no longer available") || !strings.Contains(got, "gemini-3.8-flash") || strings.Contains(got, "{") {
		t.Fatalf("got %q", got)
	}
	if got, _ := Classify("groq", &llm.HTTPError{Status: 404}); got != "model not found (pick another with /model)" {
		t.Fatalf("empty body: %q", got)
	}
}
