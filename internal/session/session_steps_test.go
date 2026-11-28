package session

import (
	"testing"

	"github.com/TonmoyTalukder/werkz/internal/llm"
)

// Claude Code's tool steps survive saving and /resume, for a model picked afterwards.
func TestStepsSurviveSave(t *testing.T) {
	h := []llm.Msg{{Role: llm.RoleUser, Text: "hi"}, {Role: llm.RoleAssistant, Text: "done", Steps: "- Read(x)\n  → abc"}}
	back := ToHistory(FromHistory(h))
	if back[1].Steps != h[1].Steps {
		t.Fatalf("steps lost: %+v", back[1])
	}
}
