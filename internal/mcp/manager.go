package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is the client version reported to servers during initialize.
var Version = "dev"

const (
	defaultConnectTimeout = 30 * time.Second
	maxResultText         = 100000
	stderrKeep            = 50
	stderrInErr           = 5
	maxFullName           = 64
)

// Tool is one tool offered by a connected server.
type Tool struct {
	Server, Name string
	FullName     string // mcp__<server>__<tool>, sanitized, at most 64 chars
	Description  string
	Schema       map[string]any
	ReadOnly     bool
	Destructive  bool
}

// State is a server's connection state.
type State string

const (
	StateConnecting State = "connecting"
	StateConnected  State = "connected"
	StateFailed     State = "failed"
	StateStopped    State = "stopped"
	StateNeedsAuth  State = "needs-auth"
)

// Status describes one server for the UI.
type Status struct {
	Name, Scope string
	State       State
	Err         string
	Tools       int
}

// Options configures a Manager.
type Options struct {
	// HTTPClient returns the client for an http/sse server (nil = http.DefaultClient).
	HTTPClient func(ServerConfig) *http.Client
	// IsAuthError classifies a connect error as "needs-auth".
	IsAuthError func(error) bool
	// Stderr receives stdio servers' stderr lines.
	Stderr func(server, line string)
}

// Result is the outcome of a tool call.
type Result struct {
	Text    string
	Images  []Image
	IsError bool
}

// Image is an image content block.
type Image struct {
	MIME string
	Data []byte
}

type server struct {
	cfg     ServerConfig
	state   State
	err     string
	tools   []Tool
	byFull  map[string]Tool
	session *sdk.ClientSession
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	stopped bool
	stderr  *stderrBuf
	listMu  sync.Mutex // serializes tool refreshes
}

// Manager owns all MCP server connections. It is safe for concurrent use.
type Manager struct {
	opts    Options
	mu      sync.RWMutex
	servers map[string]*server
	version atomic.Uint64
}

// NewManager creates an empty manager.
func NewManager(o Options) *Manager {
	return &Manager{opts: o, servers: map[string]*server{}}
}

// Version increases whenever the tool set changes.
func (m *Manager) Version() uint64 { return m.version.Load() }

func connectTimeout() time.Duration {
	if v := os.Getenv("MCP_TIMEOUT"); v != "" {
		if ms, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && ms > 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return defaultConnectTimeout
}

// Start connects to the server, initializes it and lists its tools. A running
// server with the same name is replaced.
func (m *Manager) Start(ctx context.Context, c ServerConfig) error {
	if !nameRe.MatchString(c.Name) {
		return fmt.Errorf("invalid server name %q", c.Name)
	}
	if err := c.validate(); err != nil {
		return err
	}
	s := &server{cfg: c, state: StateConnecting, stderr: &stderrBuf{}}

	timeout := connectTimeout()
	// The connection outlives Start, so it gets its own context: cancelled on
	// timeout, when ctx is cancelled during connect, or when the server stops.
	cctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	timer := time.AfterFunc(timeout, cancel)
	stopCtx := context.AfterFunc(ctx, cancel)
	cleanup := func() { timer.Stop(); stopCtx() }

	fail := func(err error) error {
		cleanup()
		timedOut, cancelled := cctx.Err() != nil, ctx.Err() != nil
		cancel()
		killTree(s.cmd)
		err = m.decorate(s, err, timedOut, cancelled, timeout)
		m.mu.Lock()
		if m.servers[c.Name] == s {
			s.state = StateFailed
			if m.opts.IsAuthError != nil && m.opts.IsAuthError(err) {
				s.state = StateNeedsAuth
			}
			s.err = err.Error()
		}
		m.mu.Unlock()
		return err
	}

	transport, err := m.transport(s)
	if err != nil {
		return fail(err)
	}

	m.mu.Lock()
	old := m.servers[c.Name]
	m.servers[c.Name] = s
	if old != nil && old.state == StateConnected && len(old.tools) > 0 {
		m.version.Add(1)
	}
	m.mu.Unlock()
	if old != nil {
		m.shutdown(old)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "werkz", Version: Version}, &sdk.ClientOptions{
		ToolListChangedHandler: func(context.Context, *sdk.ToolListChangedRequest) { go m.refresh(s) },
	})
	// Connect and list in a goroutine so a transport that ignores context
	// cancellation cannot hold Start past its deadline.
	type connected struct {
		cs    *sdk.ClientSession
		tools []Tool
		err   error
	}
	done := make(chan connected, 1)
	go func() {
		cs, err := client.Connect(cctx, transport, nil)
		if err != nil {
			done <- connected{err: err}
			return
		}
		tools, err := m.listTools(cctx, s, cs)
		if err != nil {
			_ = cs.Close()
			done <- connected{err: err}
			return
		}
		done <- connected{cs: cs, tools: tools}
	}()
	var session *sdk.ClientSession
	var tools []Tool
	select {
	case r := <-done:
		if r.err != nil {
			return fail(r.err)
		}
		session, tools = r.cs, r.tools
	case <-cctx.Done():
		go func() {
			if r := <-done; r.cs != nil {
				_ = r.cs.Close()
			}
		}()
		return fail(cctx.Err())
	}
	cleanup()

	m.mu.Lock()
	if m.servers[c.Name] != s || s.stopped {
		m.mu.Unlock()
		_ = session.Close()
		killTree(s.cmd)
		cancel()
		return errors.New("server was stopped or replaced while connecting")
	}
	s.session = session
	s.state = StateConnected
	s.setTools(tools)
	m.version.Add(1)
	m.mu.Unlock()

	go func() {
		err := session.Wait()
		m.died(s, err)
	}()
	return nil
}

// decorate turns a connect error into a friendlier one.
func (m *Manager) decorate(s *server, err error, timedOut, cancelled bool, timeout time.Duration) error {
	tail := s.stderr.tail(stderrInErr)
	switch {
	case cancelled:
		err = fmt.Errorf("cancelled: %w", err)
	case timedOut:
		err = fmt.Errorf("timed out after %s connecting to %s", timeout, s.cfg.Name)
	case s.cfg.Type == "stdio" && tail != "":
		return fmt.Errorf("server exited: %s (%w)", tail, err)
	}
	if tail != "" {
		return fmt.Errorf("%w; stderr: %s", err, tail)
	}
	return err
}

func (m *Manager) transport(s *server) (sdk.Transport, error) {
	c := s.cfg
	switch c.Type {
	case "stdio":
		cmd := exec.Command(c.Command, c.Args...)
		env := os.Environ()
		keys := make([]string, 0, len(c.Env))
		for k := range c.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			env = append(env, k+"="+c.Env[k])
		}
		cmd.Env = env
		setProcessGroup(cmd)
		cmd.WaitDelay = 2 * time.Second
		name, cb := c.Name, m.opts.Stderr
		cmd.Stderr = &lineWriter{buf: s.stderr, fn: func(l string) {
			if cb != nil {
				cb(name, l)
			}
		}}
		s.cmd = cmd
		return &sdk.CommandTransport{Command: cmd, TerminateDuration: 2 * time.Second}, nil
	case "http", "sse":
		hc := http.DefaultClient
		if m.opts.HTTPClient != nil {
			if h := m.opts.HTTPClient(c); h != nil {
				hc = h
			}
		}
		if len(c.Headers) > 0 {
			cp := *hc
			origin := ""
			if u, err := url.Parse(c.URL); err == nil {
				origin = u.Scheme + "://" + u.Host
			}
			cp.Transport = &headerTransport{base: hc.Transport, headers: c.Headers, origin: origin}
			hc = &cp
		}
		if c.Type == "sse" {
			return &sdk.SSEClientTransport{Endpoint: c.URL, HTTPClient: hc}, nil
		}
		return &sdk.StreamableClientTransport{Endpoint: c.URL, HTTPClient: hc}, nil
	}
	return nil, fmt.Errorf("unsupported server type %q", c.Type)
}

type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
	origin  string // scheme://host of the server
}

func (t *headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	// the configured headers (often keys) go only to the server's own scheme and host, never to
	// another host a redirect leads to
	if t.origin == "" || r.URL.Scheme+"://"+r.URL.Host == t.origin {
		for k, v := range t.headers {
			r.Header.Set(k, v)
		}
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(r)
}

func (m *Manager) listTools(ctx context.Context, s *server, cs *sdk.ClientSession) ([]Tool, error) {
	var out []Tool
	for t, err := range cs.Tools(ctx, nil) {
		if err != nil {
			return nil, fmt.Errorf("listing tools: %w", err)
		}
		out = append(out, convertTool(s.cfg.Name, t))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FullName < out[j].FullName })
	return out, nil
}

// setTools stores a server's tools, once per name: a server that lists a tool twice would
// otherwise make every model request fail on the duplicate.
func (s *server) setTools(ts []Tool) {
	s.tools = ts[:0:0]
	s.byFull = make(map[string]Tool, len(ts))
	for _, t := range ts {
		if _, dup := s.byFull[t.FullName]; dup {
			continue
		}
		s.byFull[t.FullName] = t
		s.tools = append(s.tools, t)
	}
}

// refresh re-lists tools after notifications/tools/list_changed.
func (m *Manager) refresh(s *server) {
	s.listMu.Lock()
	defer s.listMu.Unlock()
	m.mu.RLock()
	cs, ok := s.session, m.servers[s.cfg.Name] == s && s.state == StateConnected
	m.mu.RUnlock()
	if !ok || cs == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout())
	defer cancel()
	tools, err := m.listTools(ctx, s, cs)
	if err != nil {
		return
	}
	m.mu.Lock()
	if m.servers[s.cfg.Name] == s && s.state == StateConnected {
		s.setTools(tools)
		m.version.Add(1)
	}
	m.mu.Unlock()
}

// died handles a connection that ended on its own.
func (m *Manager) died(s *server, err error) {
	m.mu.Lock()
	if m.servers[s.cfg.Name] != s || s.stopped || s.state != StateConnected {
		m.mu.Unlock()
		return
	}
	msg := "connection closed"
	if t := s.stderr.tail(stderrInErr); t != "" {
		msg = "server exited: " + t
	} else if err != nil {
		msg = "connection closed: " + err.Error()
	}
	s.state, s.err = StateFailed, msg
	s.tools, s.byFull = nil, nil
	m.version.Add(1)
	m.mu.Unlock()
	m.shutdownProc(s)
}

func (m *Manager) shutdownProc(s *server) {
	m.mu.RLock()
	cs := s.session
	m.mu.RUnlock()
	if cs != nil {
		_ = cs.Close()
	}
	killTree(s.cmd)
	if s.cancel != nil {
		s.cancel()
	}
}

// shutdown marks s stopped and tears it down.
func (m *Manager) shutdown(s *server) {
	m.mu.Lock()
	had := s.state == StateConnected && len(s.tools) > 0
	s.stopped = true
	s.state, s.err = StateStopped, ""
	s.tools, s.byFull = nil, nil
	if had {
		m.version.Add(1)
	}
	m.mu.Unlock()
	m.shutdownProc(s)
}

// Stop stops the named server (it stays listed in Status as "stopped").
func (m *Manager) Stop(name string) error {
	m.mu.RLock()
	s := m.servers[name]
	m.mu.RUnlock()
	if s == nil {
		return fmt.Errorf("no such MCP server %q", name)
	}
	m.shutdown(s)
	return nil
}

// StopAll stops every server.
func (m *Manager) StopAll() {
	m.mu.RLock()
	all := make([]*server, 0, len(m.servers))
	for _, s := range m.servers {
		all = append(all, s)
	}
	m.mu.RUnlock()
	var wg sync.WaitGroup
	for _, s := range all {
		wg.Add(1)
		go func() { defer wg.Done(); m.shutdown(s) }()
	}
	wg.Wait()
}

// Status lists every server Start was called for, sorted by name.
func (m *Manager) Status() []Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Status, 0, len(m.servers))
	for _, s := range m.servers {
		out = append(out, Status{Name: s.cfg.Name, Scope: s.cfg.Scope, State: s.state, Err: s.err, Tools: len(s.tools)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Tools returns the tools of connected servers, sorted by FullName.
func (m *Manager) Tools() []Tool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Tool
	for _, s := range m.servers {
		if s.state == StateConnected {
			out = append(out, s.tools...)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FullName < out[j].FullName })
	return out
}

// Lookup finds a tool by its full name.
func (m *Manager) Lookup(fullName string) (Tool, bool) {
	t, _, ok := m.find(fullName)
	return t, ok
}

func (m *Manager) find(fullName string) (Tool, *sdk.ClientSession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.servers {
		if s.state != StateConnected {
			continue
		}
		if t, ok := s.byFull[fullName]; ok {
			return t, s.session, true
		}
	}
	return Tool{}, nil, false
}

// Call invokes a tool. The deadline comes from ctx.
func (m *Manager) Call(ctx context.Context, fullName string, args json.RawMessage) (*Result, error) {
	t, cs, ok := m.find(fullName)
	if !ok {
		return nil, fmt.Errorf("unknown or disconnected MCP tool %q", fullName)
	}
	var a any = map[string]any{}
	if s := strings.TrimSpace(string(args)); s != "" && s != "null" {
		if !json.Valid(args) {
			return nil, errors.New("tool arguments are not valid JSON")
		}
		a = args
	}
	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: t.Name, Arguments: a})
	if err != nil {
		return nil, err
	}
	return convertResult(res), nil
}

func convertResult(res *sdk.CallToolResult) *Result {
	out := &Result{IsError: res.IsError}
	var parts []string
	for _, c := range res.Content {
		switch c := c.(type) {
		case *sdk.TextContent:
			parts = append(parts, c.Text)
		case *sdk.ImageContent:
			out.Images = append(out.Images, Image{MIME: c.MIMEType, Data: c.Data})
		case *sdk.AudioContent:
			parts = append(parts, fmt.Sprintf("[audio %s, %d bytes]", c.MIMEType, len(c.Data)))
		case *sdk.ResourceLink:
			parts = append(parts, "[resource "+c.URI+"]")
		case *sdk.EmbeddedResource:
			if r := c.Resource; r != nil {
				line := "[resource " + r.URI + "]"
				if r.Text != "" {
					line += "\n" + r.Text
				}
				parts = append(parts, line)
			}
		}
	}
	if len(parts) == 0 && len(out.Images) == 0 && res.StructuredContent != nil {
		if b, err := json.Marshal(res.StructuredContent); err == nil {
			parts = append(parts, string(b))
		}
	}
	text := strings.Join(parts, "\n")
	if len(text) > maxResultText {
		cut := maxResultText
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + fmt.Sprintf("\n[output truncated: %d of %d characters shown]", cut, len(text))
	}
	out.Text = text
	return out
}

func convertTool(server string, t *sdk.Tool) Tool {
	out := Tool{Server: server, Name: t.Name, Description: t.Description, FullName: fullName(server, t.Name)}
	switch s := t.InputSchema.(type) {
	case map[string]any:
		out.Schema = s
	case nil:
	default:
		if b, err := json.Marshal(s); err == nil {
			_ = json.Unmarshal(b, &out.Schema)
		}
	}
	if out.Schema == nil {
		out.Schema = map[string]any{"type": "object"}
	}
	if a := t.Annotations; a != nil {
		out.ReadOnly = a.ReadOnlyHint
		out.Destructive = a.DestructiveHint != nil && *a.DestructiveHint
	}
	return out
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			return r
		}
		return '_'
	}, s)
}

// fullName builds mcp__<server>__<tool>. When sanitizing altered a part or the
// name exceeds 64 characters, it is truncated and suffixed with a hash of the
// original names so it stays deterministic and unique.
func fullName(server, tool string) string {
	s, t := sanitize(server), sanitize(tool)
	name := "mcp__" + s + "__" + t
	if s == server && t == tool && len(name) <= maxFullName {
		return name
	}
	sum := sha256.Sum256([]byte(server + "\x00" + tool))
	suffix := "_" + hex.EncodeToString(sum[:4])
	if len(name) > maxFullName-len(suffix) {
		name = name[:maxFullName-len(suffix)]
	}
	return name + suffix
}

// stderrBuf keeps the last stderrKeep lines.
type stderrBuf struct {
	mu    sync.Mutex
	lines []string
}

func (b *stderrBuf) add(l string) {
	b.mu.Lock()
	b.lines = append(b.lines, l)
	if len(b.lines) > stderrKeep {
		b.lines = b.lines[len(b.lines)-stderrKeep:]
	}
	b.mu.Unlock()
}

func (b *stderrBuf) tail(n int) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	l := b.lines
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return strings.Join(l, " | ")
}

// lineWriter splits a byte stream into lines.
type lineWriter struct {
	mu   sync.Mutex
	part []byte
	buf  *stderrBuf
	fn   func(string)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.part = append(w.part, p...)
	for {
		i := strings.IndexByte(string(w.part), '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(w.part[:i]), "\r")
		w.part = w.part[i+1:]
		w.buf.add(line)
		w.fn(line)
	}
	if len(w.part) > 64*1024 { // runaway line without newline
		line := string(w.part)
		w.part = nil
		w.buf.add(line)
		w.fn(line)
	}
	return len(p), nil
}
