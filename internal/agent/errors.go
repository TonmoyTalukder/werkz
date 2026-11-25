package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/TonmoyTalukder/werkz/internal/llm"
	"github.com/TonmoyTalukder/werkz/internal/textutil"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

// classify turns an error into a short reason; sticky errors disable the provider for the session.
func Classify(provider string, err error) (string, bool) {
	var ae *anthropic.Error
	if errors.As(err, &ae) {
		body := strings.ToLower(ae.Error())
		switch {
		case ae.StatusCode == 400 && strings.Contains(body, "credit"):
			return "out of API credits", true
		case ae.StatusCode == 401 || ae.StatusCode == 403:
			return "invalid or unauthorized API key", true
		case ae.StatusCode == 429:
			return "rate limited", false
		case ae.StatusCode == 529 || ae.StatusCode >= 500:
			return "overloaded / server error", false
		}
		return fmt.Sprintf("HTTP %d: %s", ae.StatusCode, errMessage(ae.Error())), false
	}
	var he *llm.HTTPError
	if errors.As(err, &he) {
		low := strings.ToLower(he.Body)
		switch {
		case he.Status == 401 || he.Status == 403:
			return "invalid or unauthorized API key", true
		case he.Status == 402 || strings.Contains(low, "insufficient") || strings.Contains(low, "quota"):
			return "quota / credits exhausted", he.Status == 402
		case he.Status == 429:
			return "rate limited", false
		case he.Status == 404:
			return "model not found (pick another with /model)", false
		}
		return fmt.Sprintf("HTTP %d: %s", he.Status, errMessage(he.Body)), false
	}
	var ne net.Error
	if errors.As(err, &ne) || strings.Contains(err.Error(), "connection refused") {
		if provider == "ollama" {
			return "Ollama not running (start it with `ollama serve`)", false
		}
		return "network error", false
	}
	return textutil.Truncate(err.Error(), 160), false
}

// errMessage pulls the human-readable message out of a JSON error body
// (including OpenRouter's nested provider errors) instead of showing raw JSON.
func errMessage(s string) string {
	i := strings.Index(s, "{")
	if i < 0 {
		return textutil.Truncate(s, 160)
	}
	var v struct {
		Message string `json:"message"`
		Error   struct {
			Message  string `json:"message"`
			Metadata struct {
				Raw          string `json:"raw"`
				ProviderName string `json:"provider_name"`
			} `json:"metadata"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(s[i:]), &v) != nil {
		return textutil.Truncate(s, 160)
	}
	msg := v.Error.Message
	if msg == "" {
		msg = v.Message
	}
	if raw := v.Error.Metadata.Raw; raw != "" {
		if inner := errMessage(raw); inner != "" && !strings.HasPrefix(inner, "{") && inner != msg {
			msg += ": " + inner
		}
	}
	if p := v.Error.Metadata.ProviderName; p != "" {
		msg += " (" + p + ")"
	}
	if msg == "" {
		return textutil.Truncate(s, 160)
	}
	return textutil.Truncate(msg, 160)
}
