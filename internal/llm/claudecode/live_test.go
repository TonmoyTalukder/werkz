package claudecode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/llm"
)

// TestLiveClaudeCode runs the real, signed-in `claude` (it uses a little of your Claude
// subscription). Run with: WERKZ_LIVE_CLAUDE=1 go test -run Live ./internal/llm/claudecode/
func TestLiveClaudeCode(t *testing.T) {
	if os.Getenv("WERKZ_LIVE_CLAUDE") == "" {
		t.Skip("set WERKZ_LIVE_CLAUDE=1 to run against the installed Claude Code")
	}
	bin, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude is not installed")
	}
	exe := filepath.Join(t.TempDir(), "werkz")
	if out, err := exec.Command("go", "build", "-o", exe, "../../../cmd/werkz").CombinedOutput(); err != nil {
		t.Fatalf("build werkz: %v\n%s", err, out)
	}
	work := t.TempDir()
	var mu sync.Mutex
	var asked []string
	var text strings.Builder
	c := &Client{Bin: bin, Exe: exe}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	turn, err := c.Stream(ctx, llm.Request{Model: "claude-haiku-5-5", Effort: "low", CacheKey: "live", Mode: "default",
		Dirs: []string{work}, History: []llm.Msg{{Role: llm.RoleUser,
			Text: "Create a file named hello.txt in the current folder containing exactly the word hi. Then reply with just: done"}}},
		llm.Handlers{
			OnText: func(s string) { text.WriteString(s) },
			Approve: func(_ context.Context, tc llm.ToolCall) (bool, string) {
				mu.Lock()
				asked = append(asked, tc.Name+" "+tc.Args)
				mu.Unlock()
				return true, ""
			},
		})
	if err != nil {
		t.Fatal(err)
	}
	b, ferr := os.ReadFile(filepath.Join(work, "hello.txt"))
	t.Logf("asked=%v text=%q usage=%+v", asked, turn.Text, turn.Usage)
	if ferr != nil || strings.TrimSpace(string(b)) != "hi" {
		t.Fatalf("hello.txt: %q %v", b, ferr)
	}
	if len(asked) == 0 {
		t.Fatal("Claude Code never asked werkz for permission")
	}
}
