package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TonmoyTalukder/werkz/internal/mcp"

	"github.com/charmbracelet/x/ansi"
)

func TestProjectMCPServersNeedApproval(t *testing.T) {
	proj := t.TempDir()
	m := chatModel(t, proj)
	m.mcp = mcp.NewManager(mcp.Options{})
	defer m.mcp.StopAll()
	write := func(cmd string) {
		_ = os.MkdirAll(filepath.Join(proj, ".werkz"), 0o755)
		_ = os.WriteFile(filepath.Join(proj, ".werkz", "mcp.json"), []byte(`{"mcpServers":{"db":{"command":"`+cmd+`","args":["--port","5"]}}}`), 0o644)
	}
	write("werkz-test-no-such-command")
	m.loadMCP(false)
	view := func() string { return ansi.Strip(m.View()) }
	if v := view(); !strings.Contains(v, "need your approval") {
		t.Fatalf("no approval notice:\n%s", v)
	}
	m.mcpCommand(nil)
	if v := view(); !strings.Contains(v, "needs your approval") || !strings.Contains(v, "werkz-test-no-such-command --port 5") {
		t.Fatalf("/mcp doesn't show what it runs:\n%s", v)
	}
	if st := m.mcp.Status(); len(st) != 0 {
		t.Fatalf("an unapproved project server started: %+v", st)
	}
	m.mcpCommand([]string{"approve", "db"})
	if len(m.cfg.MCPApproved) != 1 {
		t.Fatalf("approval not saved: %v", m.cfg.MCPApproved)
	}
	c, _ := m.mcpConfig("db")
	if !m.mcpApproved(c) {
		t.Fatal("approved server still needs approval")
	}
	// a changed command needs approval again
	write("something-else")
	m.loadMCP(false)
	c, _ = m.mcpConfig("db")
	if m.mcpApproved(c) {
		t.Fatal("a changed command kept its approval")
	}
}

func TestMCPLoginNeedsApprovalFirst(t *testing.T) {
	proj := t.TempDir()
	m := chatModel(t, proj)
	m.mcp = mcp.NewManager(mcp.Options{})
	defer m.mcp.StopAll()
	_ = os.MkdirAll(filepath.Join(proj, ".werkz"), 0o755)
	_ = os.WriteFile(filepath.Join(proj, ".werkz", "mcp.json"), []byte(`{"mcpServers":{"remote":{"url":"https://x.test/mcp"}}}`), 0o644)
	m.loadMCP(false)
	m.mcpCommand([]string{"login", "remote"})
	if v := ansi.Strip(m.View()); !strings.Contains(v, "haven't approved") {
		t.Fatalf("login on an unapproved server:\n%s", v)
	}
	if cmd := m.handleMCPLogin(mcpLoginMsg{name: "remote"}); cmd != nil {
		t.Fatal("a finished sign-in started an unapproved server")
	}
}
