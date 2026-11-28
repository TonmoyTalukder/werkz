package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TonmoyTalukder/werkz/internal/catalog"
	"github.com/TonmoyTalukder/werkz/internal/llm"
	"github.com/TonmoyTalukder/werkz/internal/tools"
)

func TestToolSchemaRequiredNeverNull(t *testing.T) {
	b, _ := json.Marshal(oaTools(tools.Defs))
	if strings.Contains(string(b), `"required":null`) {
		t.Fatal(`tool schema contains "required":null (Baidu rejects it)`)
	}
}

func TestOpenRouterPaidModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[
			{"id":"z-ai/paid-model","context_length":200000,"pricing":{"prompt":"0.000003","completion":"0.000015"}},
			{"id":"meta-llama/llama-3.3-70b-instruct:free","context_length":131072,"pricing":{"prompt":"0","completion":"0"}},
			{"id":"openrouter/auto","context_length":2000000,"pricing":{"prompt":"-1","completion":"-1"}}]}`)
	}))
	defer srv.Close()
	list := func(paid bool) []catalog.ModelInfo {
		ms, err := (&Client{ID: "openrouter", BaseURL: srv.URL, Key: "k", IncludePaid: paid}).ListModels(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return ms
	}
	if ms := list(false); len(ms) != 1 || !ms[0].Free {
		t.Fatalf("default should list only free models: %+v", ms)
	}
	ms := list(true)
	if len(ms) != 3 || !ms[0].Free {
		t.Fatalf("paid listing should include all, free first: %+v", ms)
	}
	for _, m := range ms[1:] {
		if m.Free {
			t.Fatalf("%s must not be free", m.ID) // includes the variable-price router (-1)
		}
		if m.ID == "z-ai/paid-model" && (m.In != 3 || m.Out != 15 || m.Context != 200000) {
			t.Fatalf("price per 1M = %v/%v ctx %d", m.In, m.Out, m.Context)
		}
	}
	if p, paid := catalog.PriceLabel(ms[2]); !paid || !strings.Contains(p, "$3") {
		t.Fatalf("label %q", p)
	}
}

func TestIsFree(t *testing.T) {
	cases := map[[2]string]bool{
		{"openrouter", "meta-llama/llama-3.3-70b-instruct:free"}: true,
		{"openrouter", "anthropic/claude-sonnet-4"}:              false,
		{"gemini", "gemini-flash-latest"}:                        true,
		{"openai", "gpt-5"}:                                      false,
	}
	for k, want := range cases {
		if got := catalog.IsFree(k[0], k[1]); got != want {
			t.Errorf("IsFree(%s, %s) = %v", k[0], k[1], got)
		}
	}
}

// M9: OpenRouter is asked for, and werkz uses, the actual cost of each request.
func TestOpenRouterReportedCost(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"cost\":0.0042}}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	c := &Client{ID: "openrouter", BaseURL: srv.URL, Key: "k"}
	turn, err := c.Stream(context.Background(), llm.Request{Model: "x/y", History: []llm.Msg{{Role: llm.RoleUser, Text: "hi"}}},
		llm.Handlers{OnText: func(string) {}, OnThinking: func(string) {}, OnToolStart: func(string) {}})
	if err != nil {
		t.Fatal(err)
	}
	if turn.Usage.CostUSD != 0.0042 {
		t.Fatalf("reported cost = %v", turn.Usage.CostUSD)
	}
	if u, _ := body["usage"].(map[string]any); u["include"] != true {
		t.Fatalf("request should ask OpenRouter for usage/cost: %v", body["usage"])
	}
}

// M13: tool calls streamed without "index" stay separate, and a stream cut off before it
// finished is an error, not a complete answer.
func TestStreamParsingEdgeCases(t *testing.T) {
	serve := func(body string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
	}
	h := llm.Handlers{OnText: func(string) {}, OnThinking: func(string) {}, OnToolStart: func(string) {}}
	req := llm.Request{Model: "m", History: []llm.Msg{{Role: llm.RoleUser, Text: "hi"}}}

	noIndex := serve("" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"a\",\"type\":\"function\",\"function\":{\"name\":\"LS\",\"arguments\":\"{\\\"path\\\":\\\"x\\\"}\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"b\",\"type\":\"function\",\"function\":{\"name\":\"Read\",\"arguments\":\"{\\\"path\\\":\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"function\":{\"arguments\":\"\\\"y\\\"}\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
	defer noIndex.Close()
	turn, err := (&Client{ID: "ollama", BaseURL: noIndex.URL}).Stream(context.Background(), req, h)
	if err != nil {
		t.Fatal(err)
	}
	if len(turn.ToolCalls) != 2 || turn.ToolCalls[0].Args != `{"path":"x"}` || turn.ToolCalls[1].Name != "Read" || turn.ToolCalls[1].Args != `{"path":"y"}` {
		t.Fatalf("tool calls merged or split wrongly: %+v", turn.ToolCalls)
	}

	cut := serve("data: {\"choices\":[{\"delta\":{\"content\":\"half an ans\"}}]}\n\n")
	defer cut.Close()
	if _, err := (&Client{ID: "ollama", BaseURL: cut.URL}).Stream(context.Background(), req, h); err == nil || !strings.Contains(err.Error(), "ended early") {
		t.Fatalf("a cut-off stream should be an error, got %v", err)
	}
}

// L10: a model that rejects tools/reasoning_effort is retried once, the user is told, and later
// requests don't send the rejected fields again.
func TestRejectedOptionsRemembered(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		if _, ok := b["tools"]; ok {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"model does not support tools or reasoning_effort"}}`)
			return
		}
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	c := &Client{ID: "ollama", BaseURL: srv.URL}
	req := llm.Request{Model: "tiny", Effort: "high", Tools: []llm.ToolDef{{Name: "LS"}},
		History: []llm.Msg{{Role: llm.RoleUser, Text: "hi"}}}
	h := llm.Handlers{OnText: func(string) {}, OnThinking: func(string) {}, OnToolStart: func(string) {}}
	turn, err := c.Stream(context.Background(), req, h)
	if err != nil || !strings.Contains(turn.Note, "can't use tools") {
		t.Fatalf("first request: err=%v note=%q", err, turn.Note)
	}
	if _, err := c.Stream(context.Background(), req, h); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 3 { // 400 + retry, then a single clean request
		t.Fatalf("requests = %d, want 3 (the rejection should be remembered)", len(bodies))
	}
	if _, ok := bodies[2]["tools"]; ok || bodies[2]["reasoning_effort"] != nil {
		t.Fatalf("rejected fields sent again: %v", bodies[2])
	}
}

// OpenCode Zen lists prices per 1M tokens; its free models answer only inside the OpenCode app,
// and image or embedding models can't chat, so werkz leaves those out.
func TestOpenCodeModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"object":"list","data":[
			{"id":"big-pickle","type":"chat","limit":{"context":200000},"cost":{"text":{"input":{"price":0},"output":{"price":0}}}},
			{"id":"kimi-k3","type":"chat","limit":{"context":1048576},"cost":{"text":{"input":{"price":2,"cache":{"read":0.5}},"output":{"price":8}}}},
			{"id":"some-image-model","type":"image","cost":{"text":{"input":{"price":1},"output":{"price":1}}}}]}`)
	}))
	defer srv.Close()
	ms, err := (&Client{ID: "opencode", BaseURL: srv.URL}).ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 || ms[0].ID != "kimi-k3" {
		t.Fatalf("models = %+v", ms)
	}
	m := ms[0]
	if m.Free || m.In != 2 || m.Out != 8 || m.CacheReadX != 0.25 || m.Context != 1048576 {
		t.Fatalf("kimi-k3 = %+v", m)
	}
}
