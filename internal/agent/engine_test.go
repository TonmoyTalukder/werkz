package agent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/TonmoyTalukder/werkz/internal/catalog"
	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/llm"
	"github.com/TonmoyTalukder/werkz/internal/llm/claude"
	"github.com/TonmoyTalukder/werkz/internal/testutil"
)

func TestFallbackAndToolLoop(t *testing.T) {
	srv := testutil.MockOpenAI(t)
	defer srv.Close()
	os.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Load()
	cfg.Keys["anthropic"] = "sk-ant-invalid-test-key"
	cfg.Keys["ollama"] = srv.URL
	cfg.Provider, cfg.Model = "anthropic", "claude-opus-5-5"
	cfg.ProviderModels["ollama"] = "mock-a"
	cfg.FallbackOrder = []string{"anthropic", "ollama"}
	cfg.Mode = "auto"
	cfg.FallbackMode = "ask"

	var events []any
	asked := 0
	e := New(cfg, func(v any) {
		if a, ok := v.(FallbackAskMsg); ok {
			asked++
			a.Reply <- FallbackChoice{Provider: a.Candidates[0].Provider, Model: a.Candidates[0].Model} // pick the suggested model
		}
		events = append(events, v)
	})
	e.Run(context.Background(), "list files", *cfg)
	if asked != 1 {
		t.Fatalf("fallback asked %d times, want 1 (approval lasts for the message)", asked)
	}

	var sawFallback, sawTool, done bool
	var text strings.Builder
	for _, ev := range events {
		switch v := ev.(type) {
		case FallbackMsg:
			sawFallback = true
			t.Logf("fallback: %s -> %s (%s)", v.From, v.To, v.Reason)
		case ToolResultMsg:
			sawTool = true
			t.Logf("tool %s: %s", v.Name, v.Summary)
		case TextDeltaMsg:
			text.WriteString(string(v))
		case TurnDoneMsg:
			done = v.Err == nil
			if v.Err != nil {
				t.Fatalf("turn error: %v", v.Err)
			}
		}
	}
	if !sawFallback || !sawTool || !done {
		t.Fatalf("fallback=%v tool=%v done=%v", sawFallback, sawTool, done)
	}
	if !strings.Contains(text.String(), "Found **files**.") {
		t.Fatalf("text = %q", text.String())
	}
	if e.disabled["anthropic"] == "" {
		t.Log("note: anthropic not marked disabled (likely network error rather than 401)")
	}
	// history should round-trip into Claude format too
	if got := len(claude.ToMessages(e.history, true)); got != 5 { // user, env system msg, assistant, tool results, assistant
		t.Fatalf("claude history messages = %d, want 5", got)
	}
}

func TestFallbackPolicy(t *testing.T) {
	srv := testutil.MockOpenAI(t)
	defer srv.Close()
	os.Setenv("XDG_CONFIG_HOME", t.TempDir())
	stop := FallbackChoice{Stop: true}
	useOllama := FallbackChoice{Provider: "ollama", Model: "mock-b"} // a model outside the chain
	mk := func(mode string, answer FallbackChoice) (*Engine, *int, *bool) {
		cfg := config.Load()
		cfg.Keys["anthropic"] = "sk-ant-invalid-test-key"
		cfg.Keys["ollama"] = srv.URL
		cfg.Provider, cfg.Model = "anthropic", "claude-haiku-5-5"
		cfg.ProviderModels["ollama"] = "mock-a"
		cfg.FallbackOrder = []string{"anthropic", "ollama"}
		cfg.Mode = "auto"
		cfg.FallbackMode = mode
		asked, switched := 0, false
		e := New(cfg, func(v any) {
			switch a := v.(type) {
			case FallbackAskMsg:
				asked++
				if len(a.Candidates) != 1 || a.Candidates[0].Model != "mock-a" {
					t.Errorf("candidates = %+v", a.Candidates)
				}
				a.Reply <- answer
			case FallbackMsg:
				switched = true
			}
		})
		return e, &asked, &switched
	}
	sys := SystemPrompt()

	if c := config.Load(); c.Fallback() != "off" {
		t.Fatalf("default fallback = %s, want off", c.Fallback())
	}
	e, asked, switched := mk("ask", stop)
	e.history = []llm.Msg{{Role: llm.RoleUser, Text: "hi"}}
	if _, err := e.callWithFallback(context.Background(), sys); err == nil || !strings.Contains(err.Error(), "declined") {
		t.Fatalf("declined fallback should stop: %v", err)
	}
	if *asked != 1 || *switched {
		t.Fatalf("ask/deny: asked=%d switched=%v", *asked, *switched)
	}

	e, asked, switched = mk("off", useOllama)
	e.history = []llm.Msg{{Role: llm.RoleUser, Text: "hi"}}
	if _, err := e.callWithFallback(context.Background(), sys); err == nil || *asked != 0 || *switched {
		t.Fatalf("off: err=%v asked=%d switched=%v", err, *asked, *switched)
	}

	e, asked, switched = mk("auto", stop)
	e.history = []llm.Msg{{Role: llm.RoleUser, Text: "hi"}}
	if _, err := e.callWithFallback(context.Background(), sys); err != nil || *asked != 0 || !*switched {
		t.Fatalf("auto: err=%v asked=%d switched=%v", err, *asked, *switched)
	}

	// ask: the user picks a model that isn't in the chain; it's used, and remembered for the message
	e, asked, switched = mk("ask", useOllama)
	srv2 := testutil.MockOpenAI(t)
	defer srv2.Close()
	e.cfg.Keys["ollama"] = srv2.URL
	e.history = []llm.Msg{{Role: llm.RoleUser, Text: "hi"}}
	var used []string
	inner := e.send
	e.send = func(v any) {
		if s, ok := v.(StreamStartMsg); ok {
			used = append(used, s.Provider+"/"+s.Model)
		}
		inner(v)
	}
	if _, err := e.callWithFallback(context.Background(), sys); err != nil || *asked != 1 || !*switched {
		t.Fatalf("ask/pick: err=%v asked=%d switched=%v", err, *asked, *switched)
	}
	if used[len(used)-1] != "ollama/mock-b" {
		t.Fatalf("picked model not used: %v", used)
	}

	if p, paid := catalog.PriceLabel(catalog.Lookup("anthropic", "claude-opus-5-5")); !paid || !strings.Contains(p, "$4") {
		t.Fatalf("opus price label %q", p)
	}
}

func TestErrMessage(t *testing.T) {
	body := `{"error":{"message":"Provider returned error","code":400,"metadata":{"raw":"{\"error\":{\"code\":\"invalid_argument\",\"message\":\"Invalid Argument\",\"type\":\"invalid_request_error\"}}","provider_name":"Baidu"}}}`
	if got := errMessage(body); got != "Provider returned error: Invalid Argument (Baidu)" {
		t.Fatalf("errMessage = %q", got)
	}
}

// H1: with fallback off, a provider disabled earlier in the session must not cause the next
// message to be sent to another provider.
func TestFallbackOffNeverSwitchesAfterDisable(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { calls++ }))
	defer srv.Close()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Load()
	cfg.Keys["anthropic"] = "k"
	cfg.Keys["ollama"] = srv.URL
	cfg.Provider, cfg.Model = "anthropic", "claude-opus-5-5"
	cfg.FallbackMode = config.FallbackOff
	var errs []error
	e := New(cfg, func(v any) {
		if d, ok := v.(TurnDoneMsg); ok && d.Err != nil {
			errs = append(errs, d.Err)
		}
	})
	e.disable("anthropic", "out of API credits")
	e.Run(context.Background(), "next message", *cfg)
	if calls != 0 {
		t.Fatalf("message was sent to another provider %d time(s) with fallback off", calls)
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "fallback is off") {
		t.Fatalf("expected a 'fallback is off' error, got %v", errs)
	}
}

// M11: after switching to a fallback model, the rest of the turn stays on it instead of
// retrying the failing main model at every tool step.
func TestFallbackPinsForTheTurn(t *testing.T) {
	var mainCalls int
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mainCalls++
		w.Header().Set("retry-after", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`)
	}))
	defer failing.Close()
	srv := testutil.MockOpenAI(t) // first call: a tool call; second: the answer
	defer srv.Close()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Load()
	cfg.Keys["anthropic"] = "k"
	cfg.Keys["ollama"] = srv.URL
	cfg.Provider, cfg.Model, cfg.Mode = "anthropic", "claude-haiku-5-5", config.ModeAuto
	cfg.FallbackMode = config.FallbackAuto
	cfg.FallbackOrder = []string{"anthropic", "ollama"}
	cfg.ProviderModels["ollama"] = "mock-a"
	var switches int
	e := New(cfg, func(v any) {
		if _, ok := v.(FallbackMsg); ok {
			switches++
		}
	})
	e.providerFor = claudeAt(failing.URL)
	e.Run(context.Background(), "list files", *cfg)
	if switches != 1 {
		t.Fatalf("switched %d times in one turn, want 1", switches)
	}
	if mainCalls > 2 { // one attempt (+1 SDK retry); not one per tool step
		t.Fatalf("failing model was called %d times in one turn", mainCalls)
	}
}

// L6: one client per provider and key is reused across requests.
func TestProviderClientsAreReused(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Load()
	cfg.Keys["anthropic"] = "k1"
	e := New(cfg, func(any) {})
	a, b := e.provider("anthropic"), e.provider("anthropic")
	if a != b {
		t.Fatal("a new Claude client was built for each request")
	}
	cfg.Keys["anthropic"] = "k2" // a different key gets its own client
	if e.provider("anthropic") == a {
		t.Fatal("changing the key must not reuse the old client")
	}
}
