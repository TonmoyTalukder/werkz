package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/llm"
	"github.com/TonmoyTalukder/werkz/internal/testutil"
)

func TestClaudeCacheLayout(t *testing.T) {
	var bodies []map[string]any
	var mu sync.Mutex
	srv := testutil.FakeClaude(t, &bodies, &mu)
	defer srv.Close()
	fakeURL := srv.URL

	os.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Load()
	cfg.Keys["anthropic"] = "sk-ant-test"
	cfg.Provider, cfg.Model, cfg.Effort = "anthropic", "claude-opus-5-5", "medium"
	var usage []UsageMsg
	e := New(cfg, func(v any) {
		if u, ok := v.(UsageMsg); ok {
			usage = append(usage, u)
		}
	})
	e.providerFor = claudeAt(fakeURL)
	e.Run(context.Background(), "first question", *cfg)
	e.Run(context.Background(), "second question", *cfg)
	e.dirList = []string{t.TempDir(), t.TempDir()} // workspace change mid-conversation
	e.Run(context.Background(), "third question", *cfg)
	e.StopKeepAlive()
	if len(bodies) != 3 {
		t.Fatalf("requests = %d", len(bodies))
	}

	prefix := func(b map[string]any) string {
		x, _ := json.Marshal(map[string]any{"tools": b["tools"], "system": b["system"], "model": b["model"]})
		return string(x)
	}
	for i, b := range bodies {
		if prefix(b) != prefix(bodies[0]) {
			t.Fatalf("request %d: tools/system prefix changed (cache would miss)", i)
		}
		sys := b["system"].([]any)[0].(map[string]any)
		if sys["cache_control"] == nil {
			t.Fatalf("no breakpoint on system block")
		}
		if b["cache_control"] == nil {
			t.Fatalf("no top-level automatic cache_control")
		}
		if strings.Contains(fmt.Sprint(b["tools"]), "eager_input_streaming") {
			t.Fatalf("eager_input_streaming in tools")
		}
		if strings.Contains(fmt.Sprint(sys["text"]), "Working directory") {
			t.Fatalf("environment leaked into the frozen system prompt")
		}
	}
	// earlier messages are re-sent unchanged (append-only history)
	m1, _ := json.Marshal(bodies[1]["messages"].([]any))
	m2, _ := json.Marshal(bodies[2]["messages"].([]any)[:len(bodies[1]["messages"].([]any))])
	if string(m1) != string(m2) {
		t.Fatalf("message history was rewritten between requests")
	}
	// env as role:system after the first user turn, and again after the workspace change
	roles := func(b map[string]any) []string {
		var r []string
		for _, m := range b["messages"].([]any) {
			r = append(r, m.(map[string]any)["role"].(string))
		}
		return r
	}
	if got := strings.Join(roles(bodies[2]), ","); got != "user,system,assistant,user,assistant,user,system" {
		t.Fatalf("roles = %s", got)
	}
	// keep-alive body: same prefix, max_tokens 0, not streamed
	var ka map[string]any
	mu.Lock()
	n := len(bodies)
	mu.Unlock()
	if _, err := e.lastKA(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	ka = bodies[n]
	mu.Unlock()
	if ka["max_tokens"] != float64(0) || ka["stream"] == true || prefix(ka) != prefix(bodies[0]) {
		t.Fatalf("keep-alive body wrong: max_tokens=%v stream=%v", ka["max_tokens"], ka["stream"])
	}
	kam, _ := json.Marshal(ka["messages"])
	lastm, _ := json.Marshal(bodies[2]["messages"])
	if string(kam) != string(lastm) {
		t.Fatal("keep-alive messages differ from the last request")
	}
	// pricing: Opus 5.5 reads at 0.05x, writes 1.25x
	got := usage[0].Cost
	want := (12*4.0 + 5000*4*0.05 + 300*4*1.25 + 7*20.0) / 1e6
	if fmt.Sprintf("%.8f", got) != fmt.Sprintf("%.8f", want) {
		t.Fatalf("cost = %v, want %v", got, want)
	}
}

func TestCompaction(t *testing.T) {
	var bodies []map[string]any
	var mu sync.Mutex
	srv := testutil.FakeClaude(t, &bodies, &mu)
	defer srv.Close()
	fakeURL := srv.URL
	os.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Load()
	cfg.Keys["anthropic"] = "sk-ant-test"
	cfg.Provider, cfg.Model, cfg.Effort = "anthropic", "claude-opus-5-5", "medium"
	var texts []string
	var done []CompactDoneMsg
	e := New(cfg, func(v any) {
		switch x := v.(type) {
		case TextDeltaMsg:
			texts = append(texts, string(x))
		case CompactDoneMsg:
			done = append(done, x)
		}
	})
	e.providerFor = claudeAt(fakeURL)
	e.Run(context.Background(), "one", *cfg)
	e.Run(context.Background(), "two", *cfg)
	e.StopKeepAlive()
	texts = nil

	// manual compaction: reuses the cached prefix and appends the instruction last
	e.Compact(context.Background(), "the auth work", *cfg)
	e.StopKeepAlive()
	last := bodies[len(bodies)-1]
	if fmt.Sprint(last["system"]) != fmt.Sprint(bodies[0]["system"]) || fmt.Sprint(last["tools"]) != fmt.Sprint(bodies[0]["tools"]) {
		t.Fatal("compaction request changed the cached prefix")
	}
	msgs := last["messages"].([]any)
	if !strings.Contains(fmt.Sprint(msgs[len(msgs)-1]), "handoff summary") || !strings.Contains(fmt.Sprint(msgs[len(msgs)-1]), "the auth work") {
		t.Fatal("instruction not appended last")
	}
	if len(texts) != 0 {
		t.Fatal("summary text leaked into the chat")
	}
	if len(e.history) != 2 || e.history[0].Role != llm.RoleUser || !strings.Contains(e.history[0].Text, "Summary of the conversation") || e.history[1].Role != llm.RoleSystem {
		t.Fatalf("history after compaction: %+v", e.history)
	}
	if len(done) != 1 || done[0].Auto {
		t.Fatalf("compactDone = %+v", done)
	}

	// auto-compaction triggers at the threshold, but holds while a test is failing
	e.Run(context.Background(), "three", *cfg)
	e.Run(context.Background(), "four", *cfg)
	e.StopKeepAlive()
	cfg.CompactAt = 1000
	e.lastContext = 5000
	e.checkFailed, e.sinceFail = true, 3
	if e.shouldAutoCompact() {
		t.Fatal("should hold while debugging a failing test")
	}
	e.lastContext = hardCompactCap + 1
	e.lastWindow = 1_000_000
	if !e.shouldAutoCompact() {
		t.Fatal("hard cap must override the debug hold")
	}
	e.checkFailed = false
	e.lastContext = 5000
	n := len(done)
	e.Run(context.Background(), "five", *cfg)
	e.StopKeepAlive()
	if len(done) != n+1 || !done[n].Auto {
		t.Fatal("auto-compaction did not run")
	}
}

// M7: when auto-compaction is due, the user's new message still reaches the model verbatim.
func TestAutoCompactKeepsNewMessageVerbatim(t *testing.T) {
	var bodies []map[string]any
	var mu sync.Mutex
	srv := testutil.FakeClaude(t, &bodies, &mu)
	defer srv.Close()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Load()
	cfg.Keys["anthropic"] = "sk-ant-test"
	cfg.Provider, cfg.Model = "anthropic", "claude-opus-5-5"
	e := New(cfg, func(any) {})
	e.providerFor = claudeAt(srv.URL)
	e.Run(context.Background(), "one", *cfg)
	e.Run(context.Background(), "two", *cfg)
	cfg.CompactAt = 1000
	e.lastContext, e.lastWindow = 5000, 1_000_000
	const exact = "rename the Foo type to Bar in every file, exactly"
	e.Run(context.Background(), exact, *cfg)
	e.StopKeepAlive()
	mu.Lock()
	last := bodies[len(bodies)-1]
	mu.Unlock()
	if !strings.Contains(fmt.Sprint(last["messages"]), exact) {
		t.Fatalf("the new message was summarised away: %v", last["messages"])
	}
	if strings.Contains(fmt.Sprint(bodies[len(bodies)-2]["messages"]), exact) {
		t.Fatal("the compaction request should not include the new message")
	}
}

// claudeServer answers every request with fn's (status, SSE or JSON body).
func claudeServer(t *testing.T, fn func(body []byte) (int, string)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		status, out := fn(b)
		if status != http.StatusOK {
			w.WriteHeader(status)
		} else {
			w.Header().Set("Content-Type", "text/event-stream")
		}
		_, _ = io.WriteString(w, out)
	}))
}

func sseMessage(stop, text string) string {
	ev := func(name, data string) string { return fmt.Sprintf("event: %s\ndata: %s\n\n", name, data) }
	s := ev("message_start", `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],"stop_reason":null,"usage":{"input_tokens":5,"output_tokens":0}}}`)
	if text != "" {
		s += ev("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		s += ev("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, text))
		s += ev("content_block_stop", `{"type":"content_block_stop","index":0}`)
	}
	s += ev("message_delta", fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q},"usage":{"output_tokens":1}}`, stop))
	return s + ev("message_stop", `{"type":"message_stop"}`)
}

// L12: an empty (refused) reply isn't stored, so the next request isn't poisoned.
func TestEmptyReplyNotStored(t *testing.T) {
	srv := claudeServer(t, func([]byte) (int, string) { return http.StatusOK, sseMessage("refusal", "") })
	defer srv.Close()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Load()
	cfg.Keys["anthropic"] = "k"
	cfg.Provider, cfg.Model = "anthropic", "claude-opus-5-5"
	e := New(cfg, func(any) {}, WithProviders(claudeAt(srv.URL)))
	e.Run(context.Background(), "hi", *cfg)
	for _, m := range e.history {
		if m.Role == llm.RoleAssistant {
			t.Fatalf("an empty assistant reply was stored: %+v", m)
		}
	}
}

// L12: when the conversation is too big for the model, /compact retries on a shrunk copy.
func TestCompactRecoversFromOverflow(t *testing.T) {
	var sizes []int
	srv := claudeServer(t, func(b []byte) (int, string) {
		sizes = append(sizes, len(b))
		if len(b) > 50_000 {
			return http.StatusBadRequest, `{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 250000 tokens > 200000 maximum"}}`
		}
		return http.StatusOK, sseMessage("end_turn", "Task: summary")
	})
	defer srv.Close()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Load()
	cfg.Keys["anthropic"] = "k"
	cfg.Provider, cfg.Model = "anthropic", "claude-opus-5-5"
	e := New(cfg, func(any) {}, WithProviders(claudeAt(srv.URL)))
	e.history = []llm.Msg{
		{Role: llm.RoleUser, Text: "read the log"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "t1", Name: "Read", Args: `{"path":"big.log"}`}}},
		{Role: llm.RoleTool, ToolCallID: "t1", ToolName: "Read", Text: strings.Repeat("log line\n", 20000)},
		{Role: llm.RoleAssistant, Text: "That's a big log."},
	}
	e.cfg = cfg
	if err := e.compact(context.Background(), "", false); err != nil {
		t.Fatalf("compaction should recover from an overflow: %v (request sizes %v)", err, sizes)
	}
	if len(sizes) < 2 || sizes[len(sizes)-1] > 50_000 {
		t.Fatalf("expected a retry with a smaller request, sizes %v", sizes)
	}
	if !strings.Contains(e.history[0].Text, "Task: summary") {
		t.Fatal("history not replaced by the summary")
	}
}
