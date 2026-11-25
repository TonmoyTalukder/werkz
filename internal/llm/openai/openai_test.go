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
		{"gemini", "gemini-2.5-flash"}:                           true,
		{"openai", "gpt-5"}:                                      false,
	}
	for k, want := range cases {
		if got := catalog.IsFree(k[0], k[1]); got != want {
			t.Errorf("IsFree(%s, %s) = %v", k[0], k[1], got)
		}
	}
}
