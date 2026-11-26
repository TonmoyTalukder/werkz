package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/llm"
)

// fakeClaude writes a stand-in for `claude -p` that records its arguments, prompt and
// environment, and prints a recorded-style stream-json conversation.
func fakeClaude(t *testing.T, events string) (bin, dir string) {
	dir = t.TempDir()
	out := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(out, []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}
	bin = filepath.Join(dir, "claude")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" >> " + filepath.Join(dir, "args") + "\n" +
		"echo '---' >> " + filepath.Join(dir, "args") + "\n" +
		"cat > " + filepath.Join(dir, "prompt") + "\n" +
		"echo \"key=${ANTHROPIC_API_KEY:-unset}\" > " + filepath.Join(dir, "env") + "\n" +
		"cat " + out + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

const conversation = `{"type":"system","subtype":"init","session_id":"s1"}
{"type":"stream_event","event":{"type":"message_start"}}
{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"Look at main.go"}}}
{"type":"stream_event","event":{"type":"content_block_start","content_block":{"type":"text"}}}
{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Reading it."}}}
{"type":"stream_event","event":{"type":"content_block_start","content_block":{"type":"tool_use","name":"Read"}}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Reading it."},{"type":"tool_use","id":"tu1","name":"Read","input":{"file_path":"/w/main.go"}}],"usage":{"input_tokens":10,"cache_read_input_tokens":5000,"output_tokens":30}}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tu1","content":"package main\nfunc main() {}"}]}}
{"type":"stream_event","event":{"type":"message_start"}}
{"type":"stream_event","event":{"type":"content_block_start","content_block":{"type":"text"}}}
{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"It is empty."}}}
{"type":"assistant","message":{"content":[{"type":"text","text":"It is empty."}],"usage":{"input_tokens":12,"cache_read_input_tokens":5100,"output_tokens":8}}}
{"type":"result","subtype":"success","is_error":false,"result":"It is empty.","session_id":"s1","usage":{"input_tokens":22,"output_tokens":38},"total_cost_usd":0.02}
`

func TestStreamShowsClaudeCodeWork(t *testing.T) {
	bin, dir := fakeClaude(t, conversation)
	t.Setenv("ANTHROPIC_API_KEY", "sk-should-not-be-used")
	c := &Client{Bin: bin, Exe: "/usr/bin/werkz"}
	var text, thinking strings.Builder
	var tools []llm.ToolCall
	var results []string
	h := llm.Handlers{
		OnText:               func(s string) { text.WriteString(s) },
		OnThinking:           func(s string) { thinking.WriteString(s) },
		OnExternalTool:       func(tc llm.ToolCall) { tools = append(tools, tc) },
		OnExternalToolResult: func(id, name string, isErr bool, out string) { results = append(results, name+":"+out) },
	}
	work := t.TempDir()
	extra := t.TempDir()
	req := llm.Request{Model: "claude-sonnet-5-5", Effort: "high", CacheKey: "w1", Mode: "acceptEdits", Window: 350_000,
		Dirs: []string{work, extra}, History: []llm.Msg{{Role: llm.RoleUser, Text: "what does main.go do?"}}}
	turn, err := c.Stream(context.Background(), req, h)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Text != "Reading it.\n\nIt is empty." || text.String() != turn.Text || thinking.String() != "Look at main.go" {
		t.Fatalf("text %q / %q thinking %q", turn.Text, text.String(), thinking.String())
	}
	if len(tools) != 1 || tools[0].Name != "Read" || !strings.Contains(tools[0].Args, `"path":"/w/main.go"`) {
		t.Fatalf("tools: %+v", tools)
	}
	if len(results) != 1 || !strings.HasPrefix(results[0], "Read:package main") {
		t.Fatalf("results: %v", results)
	}
	if turn.Usage.CacheRead != 5100 || turn.Usage.Out != 38 || turn.Usage.CostUSD != 0 {
		t.Fatalf("usage: %+v", turn.Usage)
	}
	env, _ := os.ReadFile(filepath.Join(dir, "env"))
	if strings.TrimSpace(string(env)) != "key=unset" {
		t.Fatalf("ANTHROPIC_API_KEY must not reach Claude Code (it would bill the key): %s", env)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	a := string(args)
	for _, want := range []string{"-p\n", "stream-json", "--session-id\n", "--model\nclaude-sonnet-5-5\n",
		"--effort\nhigh\n", "--permission-mode\nacceptEdits\n", "--permission-prompt-tool\nmcp__werkz__approve\n",
		"--add-dir\n" + extra + "\n", "--autocompact\n350000\n"} {
		if !strings.Contains(a, want) {
			t.Fatalf("args missing %q:\n%s", want, a)
		}
	}
	if strings.Contains(a, "--bare") {
		t.Fatal("--bare ignores the subscription sign-in")
	}

	// The next turn resumes the same Claude Code session with only the new message.
	req.History = append(req.History, llm.Msg{Role: llm.RoleAssistant, Text: turn.Text},
		llm.Msg{Role: llm.RoleUser, Text: "add a hello world"})
	if _, err := c.Stream(context.Background(), req, h); err != nil {
		t.Fatal(err)
	}
	args, _ = os.ReadFile(filepath.Join(dir, "args"))
	second := strings.SplitN(string(args), "---\n", 3)[1]
	prompt, _ := os.ReadFile(filepath.Join(dir, "prompt"))
	if !strings.Contains(second, "--resume\n") || strings.TrimSpace(string(prompt)) != "add a hello world" {
		t.Fatalf("second turn args:\n%s\nprompt %q", second, prompt)
	}
}

func TestSignedOutClaudeCode(t *testing.T) {
	bin, _ := fakeClaude(t, `{"type":"result","subtype":"success","is_error":true,"result":"Invalid API key · Please run /login"}`+"\n")
	c := &Client{Bin: bin}
	_, err := c.Stream(context.Background(), llm.Request{Dirs: []string{t.TempDir()},
		History: []llm.Msg{{Role: llm.RoleUser, Text: "hi"}}}, llm.Handlers{})
	he, ok := err.(*llm.HTTPError)
	if !ok || !he.Fatal || !strings.Contains(he.Reason, "/login") {
		t.Fatalf("expected a sign-in error, got %#v", err)
	}
}

// The bridge turns Claude Code's MCP permission calls into werkz approvals.
func TestBridgeAsksWerkz(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "p.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var asked []llm.ToolCall
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go serveApprovals(ctx, ln, llm.Handlers{Approve: func(_ context.Context, tc llm.ToolCall) (bool, string) {
		asked = append(asked, tc)
		return tc.Name != "Bash", "The user denied this action."
	}})

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go Bridge(sock, inR, outW) //nolint:errcheck
	send := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = inW.Write(append(b, '\n'))
	}
	rd := bufio.NewReader(outR)
	recv := func() map[string]any {
		done := make(chan map[string]any, 1)
		go func() {
			line, _ := rd.ReadBytes('\n')
			var m map[string]any
			_ = json.Unmarshal(line, &m)
			done <- m
		}()
		select {
		case m := <-done:
			return m
		case <-time.After(5 * time.Second):
			t.Fatal("no reply from the bridge")
			return nil
		}
	}
	send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-06-18"}})
	if r := recv(); r["result"].(map[string]any)["protocolVersion"] != "2025-06-18" {
		t.Fatalf("initialize: %v", r)
	}
	send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	if r := recv(); !strings.Contains(mustJSON(r), `"name":"approve"`) {
		t.Fatalf("tools/list: %v", r)
	}
	call := func(id int, tool string, input map[string]any) map[string]any {
		send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{
			"name": "approve", "arguments": map[string]any{"tool_name": tool, "input": input, "tool_use_id": "tu"}}})
		r := recv()
		text := r["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
		var d map[string]any
		_ = json.Unmarshal([]byte(text), &d)
		return d
	}
	if d := call(3, "Edit", map[string]any{"file_path": "/w/a.go", "old_string": "a", "new_string": "b"}); d["behavior"] != "allow" ||
		d["updatedInput"].(map[string]any)["file_path"] != "/w/a.go" {
		t.Fatalf("Edit decision: %v", d)
	}
	if d := call(4, "Bash", map[string]any{"command": "rm -rf /"}); d["behavior"] != "deny" || d["message"] == "" {
		t.Fatalf("Bash decision: %v", d)
	}
	if len(asked) != 2 || !strings.Contains(asked[0].Args, `"path":"/w/a.go"`) {
		t.Fatalf("werkz was asked: %+v", asked)
	}
	inW.Close()
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestExpiredClaudeCodeSignIn(t *testing.T) {
	bin, _ := fakeClaude(t, `{"type":"result","subtype":"success","is_error":true,"result":"Failed to authenticate: OAuth session expired and could not be refreshed"}`+"\n")
	c := &Client{Bin: bin}
	_, err := c.Stream(context.Background(), llm.Request{Dirs: []string{t.TempDir()},
		History: []llm.Msg{{Role: llm.RoleUser, Text: "hi"}}}, llm.Handlers{})
	he, ok := err.(*llm.HTTPError)
	if !ok || !he.Fatal || !strings.Contains(he.Reason, "isn't signed in") {
		t.Fatalf("expected a sign-in error, got %#v", err)
	}
}

// Claude Code reports the subscription's usage windows; werkz passes them on.
func TestRateLimitEventReportsPlanWindows(t *testing.T) {
	bin, _ := fakeClaude(t, `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1795678800,"rateLimitType":"five_hour","unifiedWindows":{"seven_day":{"utilization":0.24,"resetsAt":1795773600,"status":"allowed"},"five_hour":{"utilization":0.23,"resetsAt":1795678800,"status":"allowed"},"seven_day_fable":{"utilization":0.91,"resetsAt":1795773600,"status":"allowed_warning"}}}}
{"type":"result","subtype":"success","is_error":false,"result":"ok"}
`)
	var got llm.PlanLimits
	c := &Client{Bin: bin}
	_, err := c.Stream(context.Background(), llm.Request{Dirs: []string{t.TempDir()}, History: []llm.Msg{{Role: llm.RoleUser, Text: "hi"}}},
		llm.Handlers{OnPlanLimits: func(l llm.PlanLimits) { got = l }})
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "claudecode" || len(got.Windows) != 3 {
		t.Fatalf("limits: %+v", got)
	}
	labels := []string{got.Windows[0].Label(), got.Windows[1].Label(), got.Windows[2].Label()}
	if labels[0] != "5-hour" || labels[1] != "weekly" || labels[2] != "weekly Fable" {
		t.Fatalf("labels %v", labels)
	}
	if w := got.Windows[0]; w.Used != 0.23 || w.ResetsAt.Unix() != 1795678800 {
		t.Fatalf("5-hour window: %+v", w)
	}
}
