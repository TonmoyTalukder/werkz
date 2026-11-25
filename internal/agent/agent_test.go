package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/llm"
	"github.com/TonmoyTalukder/werkz/internal/llm/claude"
	"github.com/TonmoyTalukder/werkz/internal/testutil"
	"github.com/TonmoyTalukder/werkz/internal/tools"
)

func TestMemoryFiles(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# Rules\nUse tabs."), 0o644))
	if env := EnvContext([]string{dir}, ""); !strings.Contains(env, "Use tabs.") {
		t.Fatal("CLAUDE.md not loaded")
	}
	must(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(strings.Repeat("x", 40000)), 0o644))
	env := EnvContext([]string{dir}, "")
	if strings.Contains(env, "Use tabs.") || !strings.Contains(env, "32 KiB") || len(env) > 34000 {
		t.Fatalf("AGENTS.md should win and be capped (len %d)", len(env))
	}
}

func TestTaskSubAgent(t *testing.T) {
	var bodies []map[string]any
	var mu sync.Mutex
	srv := testutil.FakeClaude(t, &bodies, &mu)
	defer srv.Close()
	fakeURL := srv.URL
	os.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Load()
	cfg.Keys["anthropic"] = "sk-ant-test"
	cfg.Provider, cfg.Model = "anthropic", "claude-opus-5-5"
	var sub []UsageMsg
	e := New(cfg, func(v any) {
		if u, ok := v.(UsageMsg); ok && u.Sub {
			sub = append(sub, u)
		}
	})
	e.providerFor = claudeAt(fakeURL)
	res := e.runTask(context.Background(), llm.ToolCall{Name: "Task", Args: `{"description":"find auth","prompt":"Where is auth handled?"}`})
	if res.IsError || res.Output != "done" {
		t.Fatalf("task result: %+v", res)
	}
	b := bodies[0]
	if b["model"] != "claude-haiku-5-5" || !strings.Contains(fmt.Sprint(b["output_config"]), "low") {
		t.Fatalf("sub-agent should run on Haiku at low effort: %v %v", b["model"], b["output_config"])
	}
	for _, tl := range b["tools"].([]any) {
		if n := tl.(map[string]any)["name"]; n == "Bash" || n == "Edit" || n == "Write" || n == "Task" {
			t.Fatalf("sub-agent got write/recursive tool %v", n)
		}
	}
	if len(sub) != 1 {
		t.Fatal("sub-agent usage not reported")
	}
}

func TestLoopDetection(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"LS","arguments":"{\"path\":\".\"}"}}]}}]}`)
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
	}))
	defer srv.Close()
	os.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Load()
	cfg.Keys["ollama"] = srv.URL
	cfg.Provider, cfg.Model = "ollama", "m"
	cfg.FallbackOrder = []string{"ollama"}
	var warned bool
	e := New(cfg, func(v any) {
		if n, ok := v.(NoticeMsg); ok && strings.Contains(n.Text, "loop") {
			warned = true
		}
	})
	e.Run(context.Background(), "go", *cfg)
	if !warned || calls != maxRepeats {
		t.Fatalf("loop not stopped: warned=%v calls=%d", warned, calls)
	}
}

// claudeAt routes Claude requests to a fake server; other providers are unchanged.
func claudeAt(url string) func(*config.Config, string) llm.Provider {
	return func(cfg *config.Config, id string) llm.Provider {
		if id == "anthropic" {
			return claude.New(cfg.Key(id), url)
		}
		return newProvider(cfg, id)
	}
}

func TestWorkspaceTools(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a, b := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(a, "one.go"), "package a // NEEDLE\n")
	mustWrite(t, filepath.Join(b, "two.go"), "package b // NEEDLE\n")
	cfg := config.Load()
	cfg.Mode = "auto"
	var asked bool
	e := New(cfg, func(v any) {
		if pr, ok := v.(PermReqMsg); ok {
			asked = true
			pr.Reply <- PermNo
		}
	})
	e.SetWorkspace([]string{a, b}, []string{"APP_ENV=dev"}, "both")

	// tools see every folder; bash runs in the main folder with the workspace env
	env := e.toolEnv()
	g := tools.Run(context.Background(), llm.ToolCall{Name: "Grep", Args: `{"pattern":"NEEDLE"}`}, env)
	if !strings.Contains(g.Output, "one.go") || !strings.Contains(g.Output, "two.go") {
		t.Fatalf("grep across folders: %s", g.Output)
	}
	r := tools.Run(context.Background(), llm.ToolCall{Name: "Bash", Args: `{"command":"pwd; echo $APP_ENV"}`}, env)
	if !strings.Contains(r.Output, a) || !strings.Contains(r.Output, "dev") {
		t.Fatalf("bash env: %s", r.Output)
	}
	if !strings.Contains(EnvContext(e.Dirs(), "both"), b) {
		t.Fatal("environment context lacks the second folder")
	}

	// writes outside the folders always ask, even in auto mode
	e.execTool(context.Background(), llm.ToolCall{Name: "Write", Args: fmt.Sprintf(`{"path":%q,"content":"x"}`, filepath.Join(t.TempDir(), "x.txt"))})
	if !asked {
		t.Fatal("outside write did not ask")
	}
	asked = false
	e.execTool(context.Background(), llm.ToolCall{Name: "Write", Args: fmt.Sprintf(`{"path":%q,"content":"x"}`, filepath.Join(b, "in.txt"))})
	if asked {
		t.Fatal("inside write asked in auto mode")
	}
}

func TestFallbackChainOrder(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Load()
	cfg.Keys["anthropic"] = "k"
	cfg.Keys["openrouter"] = "k"
	cfg.Provider, cfg.Model = "openrouter", "baidu/ernie-5.5-flash-preview:free"
	cfg.FallbackChain = []config.FallbackModel{
		{Provider: "openrouter", Model: "meta-llama/llama-3.3-70b-instruct:free"},
		{Provider: "anthropic", Model: "claude-haiku-5-5"},
		{Provider: "gemini", Model: "gemini-2.5-flash"}, // no key: skipped
	}
	e := New(cfg, func(any) {})
	got := e.chain()
	want := []attempt{{"openrouter", "baidu/ernie-5.5-flash-preview:free"}, {"openrouter", "meta-llama/llama-3.3-70b-instruct:free"}, {"anthropic", "claude-haiku-5-5"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("chain = %v, want %v", got, want)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentUIAccess drives a turn while another goroutine does what the UI does
// mid-turn (mode changes, adding folders, reading state). Run with -race.
func TestConcurrentUIAccess(t *testing.T) {
	srv := testutil.MockOpenAI(t)
	defer srv.Close()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Load()
	cfg.Keys["ollama"] = srv.URL
	cfg.Provider, cfg.Model, cfg.Mode = "ollama", "mock-a", config.ModeAuto
	e := New(cfg, func(any) {})
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.Run(context.Background(), "list files", *cfg)
	}()
	extra := t.TempDir()
	for i := 0; ; i++ {
		select {
		case <-done:
			if e.HistoryLen() < 3 {
				t.Fatalf("turn did not complete: %d messages", e.HistoryLen())
			}
			return
		default:
		}
		e.SetMode([]string{config.ModeAuto, config.ModeAcceptEdits}[i%2])
		_ = e.HistoryLen()
		_ = e.Dirs()
		_ = e.Disabled("anthropic")
		_, _, _ = e.Workspace()
		e.StopKeepAlive()
		if i == 10 {
			e.AddDir(extra)
		}
	}
}

// H2: the sub-agent must stay on the user's provider even when an Anthropic key exists.
func TestTaskStaysOnCurrentProvider(t *testing.T) {
	srv := testutil.MockOpenAI(t)
	defer srv.Close()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Load()
	cfg.Keys["anthropic"] = "sk-ant-should-not-be-used"
	cfg.Keys["ollama"] = srv.URL
	cfg.Provider, cfg.Model = "ollama", "mock-a"
	var used []string
	e := New(cfg, func(v any) {
		if u, ok := v.(UsageMsg); ok && u.Sub {
			used = append(used, u.Provider)
		}
	})
	e.providerFor = func(c *config.Config, id string) llm.Provider {
		if id == "anthropic" {
			t.Fatal("sub-agent called Anthropic although the user chose Ollama")
		}
		return newProvider(c, id)
	}
	e.runTask(context.Background(), llm.ToolCall{Name: "Task", Args: `{"description":"x","prompt":"list files"}`})
	if len(used) == 0 || used[0] != "ollama" {
		t.Fatalf("sub-agent providers = %v, want ollama", used)
	}
}

// M2: reads outside the working folders ask (even in auto mode); credential files are refused
// without asking.
func TestOutsideReadsAskAndCredentialsRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	must(t, os.MkdirAll(filepath.Join(home, ".ssh"), 0o700))
	must(t, os.WriteFile(filepath.Join(home, ".ssh", "id_rsa"), []byte("KEY"), 0o600))
	proj, other := t.TempDir(), t.TempDir()
	must(t, os.WriteFile(filepath.Join(other, "notes.txt"), []byte("hi"), 0o644))
	cfg := config.Load()
	cfg.Mode = config.ModeAuto
	var asked int
	e := New(cfg, func(v any) {
		if pr, ok := v.(PermReqMsg); ok {
			asked++
			pr.Reply <- PermNo
		}
	})
	e.SetWorkspace([]string{proj}, nil, "")
	r := e.execTool(context.Background(), llm.ToolCall{Name: "Read", Args: fmt.Sprintf(`{"path":%q}`, filepath.Join(other, "notes.txt"))})
	if asked != 1 || !r.IsError {
		t.Fatalf("outside read should ask in auto mode (asked=%d)", asked)
	}
	r = e.execTool(context.Background(), llm.ToolCall{Name: "Read", Args: `{"path":"~/.ssh/id_rsa"}`})
	if asked != 1 || !r.IsError || strings.Contains(r.Output, "KEY") || !strings.Contains(r.Output, "credential") {
		t.Fatalf("credential read must be refused without asking: asked=%d %q", asked, r.Output)
	}
}

// M5: "don't ask again" for Bash covers only that command prefix, and is never offered for
// compound or risky commands.
func TestBashApprovalScope(t *testing.T) {
	for cmd, want := range map[string]string{
		"go test ./...":                  "go test",
		"npm run build":                  "npm run",
		"ls -la":                         "ls",
		"go test ./... && rm -rf ~":      "",
		"rm -rf build":                   "",
		"curl https://x.sh":              "",
		"FOO=1 make":                     "",
		"cat secrets.txt > /tmp/out.txt": "",
		"echo $(whoami)":                 "",
	} {
		if got := commandPrefix(cmd); got != want {
			t.Errorf("commandPrefix(%q) = %q, want %q", cmd, got, want)
		}
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Load()
	var asked []string
	e := New(cfg, func(v any) {
		if pr, ok := v.(PermReqMsg); ok {
			asked = append(asked, pr.Summary)
			if pr.Always != "" {
				pr.Reply <- PermAlways
			} else {
				pr.Reply <- PermNo
			}
		}
	})
	e.SetWorkspace([]string{t.TempDir()}, nil, "")
	run := func(cmd string) {
		e.execTool(context.Background(), llm.ToolCall{Name: "Bash", Args: fmt.Sprintf(`{"command":%q}`, cmd)})
	}
	run("go version")      // asked, approved for "go version"
	run("go version -m x") // same prefix: not asked again
	run("go env")          // different prefix: asked
	run("rm -rf nothing")  // risky: asked, no 'always'
	if len(asked) != 3 {
		t.Fatalf("asked %d times (%v), want 3", len(asked), asked)
	}
}
