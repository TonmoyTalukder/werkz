// Package testutil provides fake model servers for tests; it never calls real APIs.
package testutil

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// mockOpenAI streams a tool call on the first request and text on the second.
func MockOpenAI(t *testing.T) *httptest.Server {
	var mu sync.Mutex
	calls := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_, _ = io.WriteString(w, `{"data":[{"id":"mock-a"},{"id":"mock-b"}]}`)
			return
		}
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(s string) { fmt.Fprintf(w, "data: %s\n\n", s) }
		if n == 1 {
			send(`{"choices":[{"delta":{"content":"Let me look."}}]}`)
			send(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"LS","arguments":""}}]}}]}`)
			send(`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\".\"}"}}]}}]}`)
			send(`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
		} else {
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"role":"tool"`) {
				t.Errorf("second request lacks tool result: %s", body)
			}
			send(`{"choices":[{"delta":{"content":"Found **files**."}}]}`)
			send(`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":5}}`)
		}
		send("[DONE]")
	}))
}

// fakeClaude records request bodies and answers like the Messages API (SSE when stream:true).
func FakeClaude(_ *testing.T, bodies *[]map[string]any, mu *sync.Mutex) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var b map[string]any
		_ = json.Unmarshal(raw, &b)
		mu.Lock()
		*bodies = append(*bodies, b)
		mu.Unlock()
		usage := `{"input_tokens":12,"cache_read_input_tokens":5000,"cache_creation_input_tokens":300,"output_tokens":7}`
		if b["stream"] != true {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],"stop_reason":"max_tokens","usage":%s}`, usage)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		ev := func(name, data string) { fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data) }
		ev("message_start", fmt.Sprintf(`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],"stop_reason":null,"usage":%s}}`, usage))
		ev("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}`)
		ev("content_block_stop", `{"type":"content_block_stop","index":0}`)
		ev("message_delta", fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":%s}`, usage))
		ev("message_stop", `{"type":"message_stop"}`)
	}))
}
