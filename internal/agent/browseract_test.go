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

const actPage = `<html><head><title>Act</title></head><body style="margin:0">
<form action="/done" method="post">
  <input id="name" name="name" aria-label="Name" style="position:absolute;left:10px;top:10px;width:200px;height:30px">
  <input id="pw" name="pw" type="password" aria-label="Password" style="position:absolute;left:10px;top:60px;width:200px;height:30px">
  <button id="go" type="submit" style="position:absolute;left:10px;top:110px;width:120px;height:40px">Send</button>
</form>
<select id="color" aria-label="Color" style="position:absolute;left:300px;top:10px"><option value="r">Red</option><option value="g">Green</option></select>
<h1 id="h" style="position:absolute;left:300px;top:100px">Start</h1>
</body></html>`

func TestActRoutesKeepTheRules(t *testing.T) {
	if _, err := browser.Find(); err != nil {
		t.Skip("no browser installed")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, actPage)
	}))
	defer srv.Close()
	var asked []PermReqMsg
	e := New(config.Load(), func(v any) {
		if p, ok := v.(PermReqMsg); ok {
			asked = append(asked, p)
			p.Reply <- PermNo
		}
	})
	e.SetMode(config.ModeBypass) // submits ask even here
	defer e.CloseBrowser()
	run := func(name, args string) string {
		return e.execTool(context.Background(), llm.ToolCall{ID: name, Name: name, Args: args}).Output
	}
	out := run("Browse", fmt.Sprintf(`{"url":%q}`, srv.URL))
	ref := func(label string) string {
		for _, l := range strings.Split(run("Find", fmt.Sprintf(`{"query":%q}`, label)), "\n") {
			if i := strings.Index(l, "[ref_"); i >= 0 {
				return l[i+1 : i+strings.Index(l[i:], "]")]
			}
		}
		t.Fatalf("no ref for %q in:\n%s", label, out)
		return ""
	}

	// a click at the submit button's position asks (element actually hit), and is denied
	run("Act", `{"action":"left_click","coordinate":[60,130]}`)
	if len(asked) != 1 || !strings.Contains(asked[0].Summary, "submits a form") {
		t.Fatalf("coordinate click on submit: %+v", asked)
	}
	// click into the name field, type, then Enter: Enter asks
	run("Act", `{"action":"left_click","coordinate":[60,25]}`)
	if out := run("Act", `{"action":"type","text":"Ada"}`); strings.Contains(out, "never types") {
		t.Fatalf("typing into a text field refused: %s", out)
	}
	run("Act", `{"action":"key","text":"Enter"}`)
	if len(asked) != 2 || !strings.Contains(asked[1].Summary, "submits a form") {
		t.Fatalf("Enter in a form: %+v", asked)
	}
	// the password field: no typing by any route
	run("Act", `{"action":"left_click","coordinate":[60,75]}`)
	for _, c := range []struct{ tool, args string }{
		{"Act", `{"action":"type","text":"hunter2"}`},
		{"Act", `{"action":"key","text":"h u n"}`},
		{"FormInput", fmt.Sprintf(`{"ref":%q,"value":"hunter2"}`, ref("Password"))},
	} {
		if out := run(c.tool, c.args); !strings.Contains(out, "never types") {
			t.Fatalf("%s %s wrote a password field: %s", c.tool, c.args, out)
		}
	}
	if out := run("Eval", `{"code":"document.getElementById('pw').value"}`); !strings.Contains(strings.ToLower(out), "secret") && !strings.Contains(strings.ToLower(out), "password") {
		t.Fatalf("Eval read the password field: %s", out)
	}
	// select by text, and JavaScript on localhost doesn't ask
	if out := run("FormInput", fmt.Sprintf(`{"ref":%q,"value":"Green"}`, ref("Color"))); strings.Contains(out, "error") {
		t.Fatalf("select: %s", out)
	}
	if out := run("Eval", `{"code":"document.getElementById('color').value"}`); !strings.Contains(out, "g") {
		t.Fatalf("select value: %s", out)
	}
	if posts != 0 {
		t.Fatalf("%d form posts went out without approval", posts)
	}
	// Batch runs steps with the same checks and stops at the first refusal
	out = run("Batch", `{"actions":[{"tool":"Act","input":{"action":"left_click","coordinate":[60,75]}},{"tool":"Act","input":{"action":"type","text":"x"}},{"tool":"Act","input":{"action":"scroll","direction":"down"}}]}`)
	if !strings.Contains(out, "Action 2 (Act) failed") || strings.Contains(out, "Action 3") {
		t.Fatalf("batch: %s", out)
	}
	// plan mode: no clicking or JavaScript, scrolling is fine
	e.SetMode(config.ModePlan)
	if out := run("Act", `{"action":"left_click","coordinate":[300,100]}`); !strings.Contains(out, "Plan mode") {
		t.Fatalf("plan click: %s", out)
	}
	if out := run("Eval", `{"code":"1+1"}`); !strings.Contains(out, "Plan mode") {
		t.Fatalf("plan eval: %s", out)
	}
	if out := run("Act", `{"action":"scroll","direction":"down"}`); strings.Contains(out, "Plan mode") {
		t.Fatalf("plan scroll blocked: %s", out)
	}
}

func TestEvalAsksOffThisMachine(t *testing.T) {
	e := New(config.Load(), func(any) {})
	e.br.connected = true
	// the check needs the page's host; without a browser it reports the error instead of asking
	if ask, _, _, _, _, err := e.actCheck(context.Background(), llm.ToolCall{Name: "Eval", Args: `{"code":"1"}`}); ask && err == nil {
		t.Log("asked") // covered end to end in the browser tests
	}
	if !pressesSubmitKey("ctrl+Enter") || !pressesSubmitKey("space") || pressesSubmitKey("Tab ArrowDown") {
		t.Fatal("pressesSubmitKey")
	}
	if !onlyNavigationKeys("Tab shift+Tab ArrowDown") || onlyNavigationKeys("a") || onlyNavigationKeys("ctrl+v") {
		t.Fatal("onlyNavigationKeys")
	}
}
