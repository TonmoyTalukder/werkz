package llm

import (
	"strings"
	"testing"
)

func TestWithSteps(t *testing.T) {
	h := []Msg{{Role: RoleUser, Text: "hi"}, {Role: RoleAssistant, Text: "done"}}
	if got := WithSteps(h); &got[0] != &h[0] {
		t.Fatal("history without steps should be returned as is")
	}
	h = append(h, Msg{Role: RoleUser, Text: "fix it"}, Msg{Role: RoleAssistant, Text: "Fixed.", Steps: "- Read(x)\n  → abc"},
		Msg{Role: RoleAssistant, Steps: "- Bash(ls)\n  → a b"})
	got := WithSteps(h)
	if !strings.HasPrefix(got[3].Text, "[Tools used in this turn by Claude Code") || !strings.Contains(got[3].Text, "- Read(x)\n  → abc\n\nFixed.") || got[3].Steps != "" {
		t.Fatalf("got %q", got[3].Text)
	}
	if !strings.HasSuffix(got[4].Text, "- Bash(ls)\n  → a b") {
		t.Fatalf("steps-only message: %q", got[4].Text)
	}
	if h[3].Text != "Fixed." || h[3].Steps == "" {
		t.Fatal("the stored history must not change")
	}
}

func TestWithStepsCutsOldTurns(t *testing.T) {
	long := strings.Repeat("x", 5000)
	var h []Msg
	for range 5 {
		h = append(h, Msg{Role: RoleUser, Text: "go"}, Msg{Role: RoleAssistant, Text: "ok", Steps: long})
	}
	got := WithSteps(h)
	for i := 1; i < len(got); i += 2 {
		cut := strings.Contains(got[i].Text, "left out")
		if old := i < 4; cut != old { // the first two of five are older than the latest three
			t.Fatalf("message %d: cut %v, %d chars", i, cut, len(got[i].Text))
		}
	}
}
