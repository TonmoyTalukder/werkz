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

// Navigations the page starts itself on localhost:PORT (a link, an approved form submit) must
// go through, using the engine's real host rule.
func TestBrowserLocalNavigationsGoThrough(t *testing.T) {
	if _, err := browser.Find(); err != nil {
		t.Skip("no browser installed")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/next":
			fmt.Fprint(w, `<title>Next</title><h1>Second page</h1>`)
		case "/done":
			fmt.Fprint(w, `<title>Done</title><h1>Account created</h1>`)
		default:
			fmt.Fprint(w, `<title>Start</title><a href="/next">Go next</a><form action="/done" method="post"><button type="submit">Create</button></form>`)
		}
	}))
	defer srv.Close()
	e := New(config.Load(), func(v any) {
		if p, ok := v.(PermReqMsg); ok {
			p.Reply <- PermYes
		}
	})
	e.SetMode(config.ModeDefault)
	defer e.CloseBrowser()
	run := func(name, args string) string {
		return e.execTool(context.Background(), llm.ToolCall{ID: name, Name: name, Args: args}).Output
	}
	ref := func(out, label string) string {
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, label) && strings.Contains(l, "[ref_") {
				i := strings.Index(l, "[ref_")
				return l[i+1 : i+strings.Index(l[i:], "]")]
			}
		}
		t.Fatalf("no %q in:\n%s", label, out)
		return ""
	}
	out := run("Browse", fmt.Sprintf(`{"url":%q}`, srv.URL))
	if out := run("Click", fmt.Sprintf(`{"ref":%q}`, ref(out, "Go next"))); !strings.Contains(out, "Second page") {
		t.Fatalf("link on localhost:PORT blocked:\n%s", out)
	}
	out = run("Browse", fmt.Sprintf(`{"url":%q}`, srv.URL))
	if out := run("Click", fmt.Sprintf(`{"ref":%q}`, ref(out, "Create"))); !strings.Contains(out, "Account created") {
		t.Fatalf("approved submit blocked:\n%s", out)
	}
}

// A browser that died (crash, window closed) is replaced on the next use.
func TestBrowserRestartsAfterItDies(t *testing.T) {
	if _, err := browser.Find(); err != nil {
		t.Skip("no browser installed")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<title>Alive</title><h1>Still here</h1>`)
	}))
	defer srv.Close()
	e := New(config.Load(), func(any) {})
	e.SetMode(config.ModeDefault)
	defer e.CloseBrowser()
	browse := func() string {
		return e.execTool(context.Background(), llm.ToolCall{ID: "b", Name: "Browse", Args: fmt.Sprintf(`{"url":%q}`, srv.URL)}).Output
	}
	browse()
	b, _ := e.current()
	_ = b.Close() // dies behind the engine's back
	if running, _, _ := e.BrowserStatus(); running {
		t.Fatal("status says a dead browser is running")
	}
	if out := browse(); !strings.Contains(out, "Still here") {
		t.Fatalf("not restarted:\n%s", out)
	}
}
