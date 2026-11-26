package responses

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/TonmoyTalukder/werkz/internal/llm"
)

type fake struct {
	mu     sync.Mutex
	bodies []map[string]any
	auths  []string
	reply  func(n int, body map[string]any, w http.ResponseWriter)
}

func (f *fake) server(t *testing.T) *httptest.Server {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		f.mu.Lock()
		f.bodies = append(f.bodies, body)
		f.auths = append(f.auths, r.Header.Get("Authorization"))
		n := len(f.bodies)
		f.mu.Unlock()
		if r.URL.Path != "/v1/responses" {
			w.WriteHeader(404)
			return
		}
		f.reply(n, body, w)
	}))
	t.Cleanup(s.Close)
	return s
}

func sse(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, e := range events {
		var v struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(e), &v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", v.Type, e)
	}
}

func client(url string) *Client {
	tok := "tok-1"
	return &Client{ID: "chatgpt", BaseURL: url + "/v1",
		Token:   func(context.Context) (string, error) { return tok, nil },
		Refresh: func(context.Context) error { tok = "tok-2"; return nil }}
}

var completed = `{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1200,"output_tokens":40,"input_tokens_details":{"cached_tokens":1000}}}}`

func TestStreamTextToolsAndReplay(t *testing.T) {
	f := &fake{reply: func(n int, body map[string]any, w http.ResponseWriter) {
		sse(w,
			`{"type":"response.reasoning_summary_text.delta","delta":"Checking the file"}`,
			`{"type":"response.output_text.delta","delta":"Let me look."}`,
			`{"type":"response.output_item.done","item":{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"ENC"}}`,
			`{"type":"response.output_item.done","item":{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"Let me look."}]}}`,
			`{"type":"response.output_item.added","item":{"type":"function_call","name":"Read"}}`,
			`{"type":"response.output_item.done","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"Read","arguments":"{\"path\":\"a.go\"}"}}`,
			completed)
	}}
	c := client(f.server(t).URL)
	var text, thinking strings.Builder
	var started []string
	hist := []llm.Msg{
		{Role: llm.RoleUser, Text: "fix a.go"},
		{Role: llm.RoleSystem, Text: "Working folder: /x"},
	}
	turn, err := c.Stream(context.Background(), llm.Request{Model: "gpt-6.2-sol", System: "SYS", History: hist,
		Tools:  []llm.ToolDef{{Name: "Read", Properties: map[string]any{"path": map[string]any{"type": "string"}}}},
		Effort: "high", CacheKey: "sess"}, llm.Handlers{
		OnText:      func(s string) { text.WriteString(s) },
		OnThinking:  func(s string) { thinking.WriteString(s) },
		OnToolStart: func(n string) { started = append(started, n) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if turn.Text != "Let me look." || text.String() != "Let me look." || thinking.String() != "Checking the file" {
		t.Fatalf("text %q / %q / thinking %q", turn.Text, text.String(), thinking.String())
	}
	if len(turn.ToolCalls) != 1 || turn.ToolCalls[0].ID != "call_1" || turn.ToolCalls[0].Name != "Read" || len(started) != 1 {
		t.Fatalf("tool calls: %+v started %v", turn.ToolCalls, started)
	}
	if u := turn.Usage; u.In != 200 || u.CacheRead != 1000 || u.Out != 40 {
		t.Fatalf("usage: %+v", u)
	}

	b := f.bodies[0]
	if b["store"] != false || b["stream"] != true || b["instructions"] != "SYS" || b["model"] != "gpt-6.2-sol" ||
		b["prompt_cache_key"] != "sess" || f.auths[0] != "Bearer tok-1" {
		t.Fatalf("request: %v auth %q", b, f.auths[0])
	}
	if r, _ := b["reasoning"].(map[string]any); r["effort"] != "high" {
		t.Fatalf("reasoning: %v", b["reasoning"])
	}
	input := b["input"].([]any)
	if dev := input[1].(map[string]any); dev["role"] != "developer" {
		t.Fatalf("mid-conversation context must be a developer message: %v", dev)
	}
	tl := b["tools"].([]any)[0].(map[string]any)
	if tl["type"] != "function" || tl["name"] != "Read" {
		t.Fatalf("tools: %v", tl)
	}

	// The next request replays the exact items (without server item ids) plus the tool result.
	hist = append(hist, llm.Msg{Role: llm.RoleAssistant, Text: turn.Text, ToolCalls: turn.ToolCalls, RespItems: turn.Items},
		llm.Msg{Role: llm.RoleTool, ToolCallID: "call_1", Text: "package a"})
	items := Input(hist)
	raw, _ := json.Marshal(items)
	s := string(raw)
	if !strings.Contains(s, `"encrypted_content":"ENC"`) || !strings.Contains(s, `"type":"function_call_output"`) ||
		strings.Contains(s, `"id":"fc_1"`) || strings.Contains(s, `"id":"rs_1"`) {
		t.Fatalf("replayed input: %s", s)
	}
}

func TestRefreshOn401(t *testing.T) {
	f := &fake{reply: func(n int, body map[string]any, w http.ResponseWriter) {
		if n == 1 {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"detail":"expired"}`)
			return
		}
		sse(w, `{"type":"response.output_text.delta","delta":"ok"}`, completed)
	}}
	c := client(f.server(t).URL)
	turn, err := c.Stream(context.Background(), llm.Request{Model: "m", History: []llm.Msg{{Role: llm.RoleUser, Text: "hi"}}}, llm.Handlers{})
	if err != nil || turn.Text != "ok" || f.auths[1] != "Bearer tok-2" {
		t.Fatalf("turn %+v err %v auths %v", turn, err, f.auths)
	}
}

func TestUnsupportedFieldIsDroppedAndRemembered(t *testing.T) {
	f := &fake{reply: func(n int, body map[string]any, w http.ResponseWriter) {
		if _, has := body["reasoning"]; has {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"code":"subscription_sharing_unsupported_capability","param":"reasoning.summary","message":"no"}}`)
			return
		}
		sse(w, completed)
	}}
	c := client(f.server(t).URL)
	req := llm.Request{Model: "m", History: []llm.Msg{{Role: llm.RoleUser, Text: "hi"}}}
	if _, err := c.Stream(context.Background(), req, llm.Handlers{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Stream(context.Background(), req, llm.Handlers{}); err != nil {
		t.Fatal(err)
	}
	if len(f.bodies) != 3 { // rejected once, then never sent again
		t.Fatalf("requests: %d", len(f.bodies))
	}
}

func TestUsageLimitMidStream(t *testing.T) {
	f := &fake{reply: func(n int, body map[string]any, w http.ResponseWriter) {
		sse(w, `{"type":"response.failed","response":{"status":"failed","error":{"code":"subscription_sharing_usage_limit_exceeded","message":"limit"}}}`)
	}}
	c := client(f.server(t).URL)
	_, err := c.Stream(context.Background(), llm.Request{Model: "m", History: []llm.Msg{{Role: llm.RoleUser, Text: "hi"}}}, llm.Handlers{})
	var he *llm.HTTPError
	if !asHTTP(err, &he) || he.Status != 429 || !he.Fatal || !strings.Contains(he.Reason, "chatgpt.com/settings/usage") {
		t.Fatalf("expected a usage-limit error, got %#v", err)
	}
}

func TestStreamWithoutCompletionIsAnError(t *testing.T) {
	f := &fake{reply: func(n int, body map[string]any, w http.ResponseWriter) {
		sse(w, `{"type":"response.output_text.delta","delta":"par"}`)
	}}
	c := client(f.server(t).URL)
	if _, err := c.Stream(context.Background(), llm.Request{Model: "m", History: []llm.Msg{{Role: llm.RoleUser, Text: "hi"}}}, llm.Handlers{}); err == nil {
		t.Fatal("a stream that ends without response.completed must fail")
	}
}

func asHTTP(err error, he **llm.HTTPError) bool {
	if e, ok := err.(*llm.HTTPError); ok {
		*he = e
		return true
	}
	return false
}
