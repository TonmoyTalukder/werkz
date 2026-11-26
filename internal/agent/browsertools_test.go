package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TonmoyTalukder/werkz/internal/browser"
	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/llm"
)

const formPage = `<html><head><title>Signup</title></head><body>
<h1>Sign up</h1>
<form action="/done" method="post">
  <label>Email <input name="email" type="email"></label>
  <label>Password <input name="pw" type="password"></label>
  <button type="submit">Create account</button>
</form>
<button id="plain" onclick="document.querySelector('h1').textContent='Clicked'">Say hi</button>
<a href="https://example.com/">Elsewhere</a>
<script>console.error("boom from page")</script>
</body></html>`

func TestBrowserToolRules(t *testing.T) {
	if _, err := browser.Find(); err != nil {
		t.Skip("no browser installed")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, formPage)
	}))
	defer srv.Close()

	cfg := config.Load()
	var asked []PermReqMsg
	answer := PermNo
	e := New(cfg, func(v any) {
		if p, ok := v.(PermReqMsg); ok {
			asked = append(asked, p)
			p.Reply <- answer
		}
	})
	e.SetMode(config.ModeBypass) // bypass: forms still ask
	defer e.CloseBrowser()
	run := func(name, args string) string {
		res := e.execTool(context.Background(), llm.ToolCall{ID: name, Name: name, Args: args})
		return res.Output
	}
	ref := func(out, label string) string {
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, label) {
				if i := strings.Index(l, "[ref_"); i >= 0 {
					return l[i+1 : i+strings.Index(l[i:], "]")]
				}
			}
		}
		t.Fatalf("no ref for %q in:\n%s", label, out)
		return ""
	}

	out := run("Browse", fmt.Sprintf(`{"url":%q}`, srv.URL))
	if len(asked) != 0 || !strings.Contains(out, "Sign up") || !strings.Contains(out, "console error") {
		t.Fatalf("localhost: asked=%d\n%s", len(asked), out)
	}
	if out := run("Click", fmt.Sprintf(`{"ref":%q}`, ref(out, "Say hi"))); !strings.Contains(out, "Clicked") || len(asked) != 0 {
		t.Fatalf("plain click: asked=%d\n%s", len(asked), out)
	}
	out = run("ReadPage", `{}`)
	if out := run("Type", fmt.Sprintf(`{"ref":%q,"text":"hunter2"}`, ref(out, "Password"))); !strings.Contains(out, "never types into password") {
		t.Fatalf("password typed: %s", out)
	}
	run("Click", fmt.Sprintf(`{"ref":%q}`, ref(out, "Create account")))
	if len(asked) != 1 || !strings.Contains(asked[0].Summary, "submits a form") || asked[0].Always != "" {
		t.Fatalf("submit (bypass) asks = %+v", asked)
	}
	if !strings.Contains(run("ReadPage", `{}`), "Clicked") {
		t.Fatal("denied submit left the page")
	}
	// another site asks, even before anything loads; denied means nothing opens
	e.SetMode(config.ModeDefault)
	out = run("Browse", `{"url":"https://example.com/"}`)
	if len(asked) != 2 || !strings.Contains(asked[1].Always, "example.com") || !strings.Contains(out, "denied") {
		t.Fatalf("other site: %+v\n%s", asked, out)
	}
	// plan mode: no clicking
	e.SetMode(config.ModePlan)
	if out := run("Click", `{"ref":"ref_1"}`); !strings.Contains(out, "Plan mode") {
		t.Fatalf("plan click: %s", out)
	}
	if out := run("Console", `{"errors_only":true}`); !strings.Contains(out, "boom from page") {
		t.Fatalf("console: %s", out)
	}
}

// Approvals given for werkz's own browser don't carry into the user's Chrome (and back).
func TestBrowserApprovalsDontCarryAcrossBrowsers(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	e := New(config.Load(), func(any) {})
	key := e.approvalKey("Browse", "github.com")
	e.allow[key] = true
	e.br.hosts.Store("github.com", true)
	e.forgetBrowserApprovals() // what connecting and disconnecting do
	if e.allow[e.approvalKey("Browse", "github.com")] {
		t.Fatal("a site approval carried over to the other browser")
	}
	if _, ok := e.br.hosts.Load("github.com"); ok {
		t.Fatal("approved hosts kept")
	}
	if e.allowHost("github.com:443") {
		t.Fatal("the page may still navigate to the old browser's approved site")
	}
}
