package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Approvals follow the file as written: a header change needs a new approval, a variable's value
// changing doesn't.
func TestHashFollowsTheFileAsWritten(t *testing.T) {
	dir := t.TempDir()
	write := func(header string) {
		_ = os.MkdirAll(filepath.Join(dir, ".werkz"), 0o755)
		_ = os.WriteFile(filepath.Join(dir, ".werkz", "mcp.json"), []byte(`{"mcpServers":{"s":{"url":"https://x.test/mcp","headers":{"Authorization":"`+header+`"}}}}`), 0o644)
	}
	hash := func() string {
		cfgs, errs := LoadConfig(filepath.Join(dir, "none.json"), dir)
		if len(errs) > 0 || len(cfgs) != 1 {
			t.Fatalf("load: %v %v", cfgs, errs)
		}
		return cfgs[0].Hash()
	}
	t.Setenv("WERKZ_TEST_TOKEN", "one")
	write("Bearer ${WERKZ_TEST_TOKEN}")
	h1 := hash()
	t.Setenv("WERKZ_TEST_TOKEN", "two")
	if hash() != h1 {
		t.Fatal("a variable's value changed the approval")
	}
	write("Bearer ${AWS_SECRET_ACCESS_KEY}")
	if hash() == h1 {
		t.Fatal("a changed header kept its approval")
	}
	cfgs, _ := LoadConfig(filepath.Join(dir, "none.json"), dir)
	if d := cfgs[0].Describe(); !strings.Contains(d, "Authorization: Bearer ${AWS_SECRET_ACCESS_KEY}") {
		t.Fatalf("describe hides the header: %s", d)
	}
}

func TestDuplicateToolsListedOnce(t *testing.T) {
	s := &server{}
	s.setTools([]Tool{{FullName: "mcp__a__x"}, {FullName: "mcp__a__x"}, {FullName: "mcp__a__y"}})
	if len(s.tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(s.tools))
	}
}
