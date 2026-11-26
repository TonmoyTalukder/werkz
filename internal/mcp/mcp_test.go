package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoIn struct {
	Text string `json:"text"`
}

func fakeServer() *sdk.Server {
	srv := sdk.NewServer(&sdk.Implementation{Name: "fake", Version: "1"}, nil)
	sdk.AddTool(srv, &sdk.Tool{Name: "echo", Description: "echo text", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}},
		func(_ context.Context, _ *sdk.CallToolRequest, in echoIn) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "echo: " + in.Text}}}, nil, nil
		})
	sdk.AddTool(srv, &sdk.Tool{Name: "fail", Description: "always fails"},
		func(context.Context, *sdk.CallToolRequest, echoIn) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: "nope"}}}, nil, nil
		})
	sdk.AddTool(srv, &sdk.Tool{Name: "grow", Description: "adds a tool"},
		func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
			sdk.AddTool(srv, &sdk.Tool{Name: "extra"}, func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
				return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "extra"}}}, nil, nil
			})
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "grown"}}}, nil, nil
		})
	sdk.AddTool(srv, &sdk.Tool{Name: "crash", Description: "exits"},
		func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
			fmt.Fprintln(os.Stderr, "fatal: crashing now")
			time.Sleep(50 * time.Millisecond)
			os.Exit(3)
			return nil, nil, nil
		})
	sdk.AddTool(srv, &sdk.Tool{Name: "pid", Description: "pid"},
		func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: fmt.Sprint(os.Getpid())}}}, nil, nil
		})
	sdk.AddTool(srv, &sdk.Tool{Name: "big", Description: "big"},
		func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: strings.Repeat("x", 150000)}}}, nil, nil
		})
	return srv
}

func TestMain(m *testing.M) {
	switch os.Getenv("WERKZ_MCP_FAKE") {
	case "1":
		fmt.Fprintln(os.Stderr, "fake server booting")
		if err := fakeServer().Run(context.Background(), &sdk.StdioTransport{}); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	case "exit":
		fmt.Fprintln(os.Stderr, "missing API key")
		os.Exit(2)
	}
	os.Exit(m.Run())
}

func stdioCfg(name string, env map[string]string) ServerConfig {
	if env == nil {
		env = map[string]string{}
	}
	if _, ok := env["WERKZ_MCP_FAKE"]; !ok {
		env["WERKZ_MCP_FAKE"] = "1"
	}
	return ServerConfig{Name: name, Type: "stdio", Command: os.Args[0], Env: env, Scope: "user"}
}

func ctx5(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return c
}

func waitFor(t *testing.T, what string, f func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if f() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestStdioLifecycle(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	m := NewManager(Options{Stderr: func(s, l string) { mu.Lock(); lines = append(lines, s+":"+l); mu.Unlock() }})
	defer m.StopAll()
	v0 := m.Version()
	if err := m.Start(ctx5(t), stdioCfg("fake", nil)); err != nil {
		t.Fatal(err)
	}
	if m.Version() == v0 {
		t.Error("version did not bump on start")
	}
	tools := m.Tools()
	names := []string{}
	for _, tl := range tools {
		names = append(names, tl.FullName)
	}
	if got := strings.Join(names, ","); got != "mcp__fake__big,mcp__fake__crash,mcp__fake__echo,mcp__fake__fail,mcp__fake__grow,mcp__fake__pid" {
		t.Fatalf("tools = %s", got)
	}
	echo, ok := m.Lookup("mcp__fake__echo")
	if !ok || !echo.ReadOnly || echo.Server != "fake" || echo.Name != "echo" || echo.Schema["type"] != "object" {
		t.Fatalf("echo = %+v ok=%v", echo, ok)
	}
	if st := m.Status(); len(st) != 1 || st[0].State != StateConnected || st[0].Tools != 6 || st[0].Scope != "user" {
		t.Fatalf("status = %+v", st)
	}
	r, err := m.Call(ctx5(t), "mcp__fake__echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil || r.Text != "echo: hi" || r.IsError {
		t.Fatalf("call = %+v %v", r, err)
	}
	r, err = m.Call(ctx5(t), "mcp__fake__fail", json.RawMessage(`{"text":"x"}`))
	if err != nil || !r.IsError || r.Text != "nope" {
		t.Fatalf("fail call = %+v %v", r, err)
	}
	r, err = m.Call(ctx5(t), "mcp__fake__big", nil)
	if err != nil || !strings.Contains(r.Text, "output truncated") || len(r.Text) > 100200 {
		t.Fatalf("big call len=%d err=%v", len(r.Text), err)
	}
	if _, err := m.Call(ctx5(t), "mcp__fake__nope", nil); err == nil {
		t.Error("expected error for unknown tool")
	}
	mu.Lock()
	if len(lines) == 0 || lines[0] != "fake:fake server booting" {
		t.Errorf("stderr lines = %v", lines)
	}
	mu.Unlock()

	// tools/list_changed
	v1 := m.Version()
	if _, err := m.Call(ctx5(t), "mcp__fake__grow", nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "extra tool", func() bool { _, ok := m.Lookup("mcp__fake__extra"); return ok })
	if m.Version() == v1 {
		t.Error("version did not bump on list_changed")
	}

	// Stop kills the process.
	r, _ = m.Call(ctx5(t), "mcp__fake__pid", nil)
	var pid int
	if _, err := fmt.Sscan(r.Text, &pid); err != nil {
		t.Fatal(err)
	}
	v2 := m.Version()
	if err := m.Stop("fake"); err != nil {
		t.Fatal(err)
	}
	if m.Version() == v2 || len(m.Tools()) != 0 {
		t.Error("stop did not bump version / remove tools")
	}
	if st := m.Status(); st[0].State != StateStopped {
		t.Errorf("status after stop = %+v", st)
	}
	waitFor(t, "process gone", func() bool { return syscall.Kill(pid, 0) != nil })
	if _, err := m.Call(ctx5(t), "mcp__fake__echo", nil); err == nil {
		t.Error("call after stop should fail")
	}
	if err := m.Stop("missing"); err == nil {
		t.Error("stop of unknown server should fail")
	}
}

func TestStdioCrash(t *testing.T) {
	m := NewManager(Options{})
	defer m.StopAll()
	if err := m.Start(ctx5(t), stdioCfg("fake", nil)); err != nil {
		t.Fatal(err)
	}
	v := m.Version()
	_, _ = m.Call(ctx5(t), "mcp__fake__crash", nil)
	waitFor(t, "failed state", func() bool { return m.Status()[0].State == StateFailed })
	st := m.Status()[0]
	if !strings.Contains(st.Err, "server exited") || !strings.Contains(st.Err, "crashing now") || st.Tools != 0 {
		t.Errorf("status = %+v", st)
	}
	if len(m.Tools()) != 0 || m.Version() == v {
		t.Error("tools not removed / version not bumped")
	}
}

func TestStdioStartFailure(t *testing.T) {
	m := NewManager(Options{})
	err := m.Start(ctx5(t), stdioCfg("bad", map[string]string{"WERKZ_MCP_FAKE": "exit"}))
	if err == nil || !strings.Contains(err.Error(), "server exited: missing API key") {
		t.Fatalf("err = %v", err)
	}
	if st := m.Status()[0]; st.State != StateFailed || !strings.Contains(st.Err, "missing API key") {
		t.Errorf("status = %+v", st)
	}
	err = m.Start(ctx5(t), ServerConfig{Name: "nocmd", Type: "stdio", Command: "/nonexistent/werkz-mcp"})
	if err == nil {
		t.Error("expected error for missing command")
	}
}

func TestStartReplaces(t *testing.T) {
	m := NewManager(Options{})
	defer m.StopAll()
	for i := 0; i < 2; i++ {
		if err := m.Start(ctx5(t), stdioCfg("fake", nil)); err != nil {
			t.Fatal(err)
		}
	}
	if len(m.Status()) != 1 || len(m.Tools()) != 6 {
		t.Errorf("status=%+v tools=%d", m.Status(), len(m.Tools()))
	}
}

func httpFake(t *testing.T) *httptest.Server {
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return fakeServer() }, nil)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test") != "yes" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestHTTP(t *testing.T) {
	ts := httpFake(t)
	m := NewManager(Options{})
	defer m.StopAll()
	cfg := ServerConfig{Name: "web", Type: "http", URL: ts.URL, Headers: map[string]string{"X-Test": "yes"}, Scope: "project"}
	if err := m.Start(ctx5(t), cfg); err != nil {
		t.Fatal(err)
	}
	r, err := m.Call(ctx5(t), "mcp__web__echo", json.RawMessage(`{"text":"over http"}`))
	if err != nil || r.Text != "echo: over http" {
		t.Fatalf("call = %+v %v", r, err)
	}
	if st := m.Status()[0]; st.State != StateConnected || st.Tools != 6 {
		t.Errorf("status = %+v", st)
	}

	// Without the header the server answers 401.
	var sawAuth bool
	m2 := NewManager(Options{IsAuthError: func(error) bool { sawAuth = true; return true }})
	bad := cfg
	bad.Name, bad.Headers = "noauth", nil
	if err := m2.Start(ctx5(t), bad); err == nil {
		t.Fatal("expected error without header")
	}
	if st := m2.Status()[0]; st.State != StateNeedsAuth || !sawAuth {
		t.Errorf("status = %+v", st)
	}

	m3 := NewManager(Options{})
	bad.Name = "plain"
	if err := m3.Start(ctx5(t), bad); err == nil || m3.Status()[0].State != StateFailed {
		t.Errorf("expected failed state, err=%v", err)
	}
}

func TestConnectTimeout(t *testing.T) {
	t.Setenv("MCP_TIMEOUT", "300")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer ts.Close()
	m := NewManager(Options{})
	start := time.Now()
	err := m.Start(ctx5(t), ServerConfig{Name: "slow", Type: "http", URL: ts.URL})
	if err == nil || !strings.Contains(err.Error(), "timed out") || time.Since(start) > 3*time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
}

func TestFullName(t *testing.T) {
	if got := fullName("srv", "do-it_1"); got != "mcp__srv__do-it_1" {
		t.Error(got)
	}
	a, b := fullName("srv", "a.b"), fullName("srv", "a b")
	if a == b || strings.ContainsAny(a, ". ") {
		t.Errorf("a=%q b=%q", a, b)
	}
	long := fullName("srv", strings.Repeat("t", 100))
	if len(long) != 64 || long != fullName("srv", strings.Repeat("t", 100)) {
		t.Errorf("long = %q (%d)", long, len(long))
	}
	if long == fullName("srv", strings.Repeat("t", 99)+"u") {
		t.Error("long names collide")
	}
}

func TestConvertResult(t *testing.T) {
	r := convertResult(&sdk.CallToolResult{Content: []sdk.Content{
		&sdk.TextContent{Text: "a"},
		&sdk.ImageContent{MIMEType: "image/png", Data: []byte{1, 2}},
		&sdk.ResourceLink{URI: "file:///x"},
		&sdk.EmbeddedResource{Resource: &sdk.ResourceContents{URI: "file:///y", Text: "body"}},
	}})
	if r.Text != "a\n[resource file:///x]\n[resource file:///y]\nbody" || len(r.Images) != 1 || r.Images[0].MIME != "image/png" {
		t.Errorf("%+v", r)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "user.json")
	proj := filepath.Join(dir, "proj")
	t.Setenv("MCP_TOK", "s3cret")
	write(t, user, `{"mcpServers":{
	  "alpha":{"command":"npx","args":["-y","pkg","${MCP_TOK}"],"env":{"K":"${MCP_TOK}","D":"${MCP_UNSET_X:-fallback}"}},
	  "shared":{"url":"https://user.example/mcp"},
	  "bad name!":{"command":"x"},
	  "nothing":{}
	}}`)
	write(t, filepath.Join(proj, ".werkz", "mcp.json"), `{"mcpServers":{
	  "shared":{"type":"sse","url":"https://proj.example/sse","headers":{"Authorization":"Bearer ${MCP_TOK}"}},
	  "zed":{"type":"stdio","command":"zed-mcp"}
	}}`)
	got, errs := LoadConfig(user, proj)
	if len(errs) != 2 {
		t.Errorf("errs = %v", errs)
	}
	if len(got) != 3 || got[0].Name != "alpha" || got[1].Name != "shared" || got[2].Name != "zed" {
		t.Fatalf("got = %+v", got)
	}
	a := got[0]
	if a.Type != "stdio" || a.Args[2] != "s3cret" || a.Env["K"] != "s3cret" || a.Env["D"] != "fallback" || a.Scope != "user" || a.Source != user {
		t.Errorf("alpha = %+v", a)
	}
	s := got[1]
	if s.Scope != "project" || s.Type != "sse" || s.URL != "https://proj.example/sse" || s.Headers["Authorization"] != "Bearer s3cret" {
		t.Errorf("shared = %+v", s)
	}

	// url-only → http; a bad file does not stop the other; missing files are fine.
	write(t, user, `{"mcpServers":{"w":{"url":"http://x"}}}`)
	write(t, filepath.Join(proj, ".werkz", "mcp.json"), `{not json`)
	got, errs = LoadConfig(user, proj)
	if len(errs) != 1 || len(got) != 1 || got[0].Type != "http" {
		t.Errorf("got=%+v errs=%v", got, errs)
	}
	got, errs = LoadConfig(filepath.Join(dir, "missing.json"), filepath.Join(dir, "none"))
	if len(got) != 0 || len(errs) != 0 {
		t.Errorf("got=%+v errs=%v", got, errs)
	}
	if p := UserConfigPath(); p != "" && !strings.HasSuffix(p, filepath.Join("werkz", "mcp.json")) {
		t.Errorf("UserConfigPath = %q", p)
	}
}

func TestHash(t *testing.T) {
	a := ServerConfig{Name: "x", Type: "stdio", Command: "a", Args: []string{"1"}, Env: map[string]string{"K": "v"}}
	if h := a.Hash(); h != a.Hash() || len(h) != 64 {
		t.Fatal("unstable")
	}
	for name, mod := range map[string]func(*ServerConfig){
		"command": func(c *ServerConfig) { c.Command = "b" },
		"args":    func(c *ServerConfig) { c.Args = []string{"2"} },
		"env":     func(c *ServerConfig) { c.Env = map[string]string{"K": "w"} },
		"url":     func(c *ServerConfig) { c.URL = "http://x" },
		"headers": func(c *ServerConfig) { c.Headers = map[string]string{"H": "1"} },
	} {
		b := a
		mod(&b)
		if a.Hash() == b.Hash() {
			t.Errorf("hash unchanged after changing %s", name)
		}
	}
	h1 := ServerConfig{Type: "http", URL: "u", Headers: map[string]string{"H": "1"}}
	h2 := ServerConfig{Type: "http", URL: "u", Headers: map[string]string{"H": "2"}}
	if h1.Hash() != h2.Hash() {
		t.Error("header values must not affect the hash")
	}
}
