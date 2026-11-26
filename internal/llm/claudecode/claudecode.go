// Package claudecode runs the user's own installed Claude Code (`claude -p`, headless mode) as a
// werkz backend, so requests use the user's Claude subscription through Claude Code itself.
// werkz never reads or handles the Claude sign-in: the unmodified `claude` program does.
//
// Claude Code runs its own agent loop and tools. werkz shows its text, thinking and tool calls,
// and answers its permission questions through a small MCP server (see Bridge), so the usual
// werkz permission dialog and rules apply.
package claudecode

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/llm"
	"github.com/TonmoyTalukder/werkz/internal/textutil"
)

// BridgeArg is the hidden werkz sub-command that serves the permission MCP server.
const BridgeArg = "__claude-code-permissions"

// Client is the Claude Code backend.
type Client struct {
	Bin string // path of the `claude` command
	Exe string // path of the werkz executable (for the permission bridge)

	mu       sync.Mutex
	sessions map[string]*session // by werkz session (Request.CacheKey)
}

// session tracks the Claude Code conversation behind one werkz conversation.
type session struct {
	id   string // Claude Code session ID
	sent int    // werkz history messages already in that Claude Code session
	root string // fingerprint of the first message: a different one means a new conversation
}

// Stream runs one werkz turn through Claude Code.
func (c *Client) Stream(ctx context.Context, req llm.Request, h llm.Handlers) (*llm.Turn, error) {
	if c.Bin == "" {
		return nil, &llm.HTTPError{Status: 404, Reason: "Claude Code isn't installed (see code.claude.com)", Fatal: true}
	}
	key := req.CacheKey
	if key == "" {
		key = "default"
	}
	c.mu.Lock()
	if c.sessions == nil {
		c.sessions = map[string]*session{}
	}
	s := c.sessions[key]
	root := fingerprint(req.History)
	resume := s != nil && s.root == root && s.sent <= len(req.History)
	var prompt string
	if resume {
		prompt = continuation(req.History[s.sent:])
	} else {
		s = &session{id: newUUID(), root: root}
		c.sessions[key] = s
		prompt = fresh(req.History)
	}
	c.mu.Unlock()

	turn, err := c.run(ctx, req, h, s.id, resume, prompt)
	if err != nil && resume && errors.Is(err, errNoSession) {
		// Claude Code no longer has that conversation: start again with the whole history.
		c.mu.Lock()
		s.id = newUUID()
		c.mu.Unlock()
		turn, err = c.run(ctx, req, h, s.id, false, fresh(req.History))
	}
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	s.sent = len(req.History) + 1 // + the assistant message werkz stores for this turn
	c.mu.Unlock()
	return turn, nil
}

var errNoSession = errors.New("claude code session not found")

func fingerprint(h []llm.Msg) string {
	for _, m := range h {
		if m.Role == llm.RoleUser {
			sum := sha256.Sum256([]byte(m.Text))
			return hex.EncodeToString(sum[:8])
		}
	}
	return ""
}

// fresh is the prompt for a new Claude Code session: the latest request, preceded by the
// conversation so far when there is one (e.g. after switching to Claude Code mid-conversation).
func fresh(h []llm.Msg) string {
	last := -1
	for i, m := range h {
		if m.Role == llm.RoleUser {
			last = i
		}
	}
	if last < 0 {
		return continuation(h)
	}
	if earlier := transcript(h[:last]); earlier != "" {
		return "Conversation so far (from werkz, possibly with another model):\n\n" + earlier +
			"\n\n---\n\n" + continuation(h[last:])
	}
	return continuation(h[last:])
}

// continuation is what to send to an existing session: new user messages and context notes,
// plus anything another model said in between.
func continuation(h []llm.Msg) string {
	var b strings.Builder
	for _, m := range h {
		switch m.Role {
		case llm.RoleUser:
			b.WriteString(m.Text + "\n\n")
		case llm.RoleSystem:
			b.WriteString("<system-reminder>\n" + m.Text + "\n</system-reminder>\n\n")
		case llm.RoleAssistant:
			if m.Text != "" {
				b.WriteString("(Answer from another model meanwhile:)\n" + m.Text + "\n\n")
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func transcript(h []llm.Msg) string {
	var b strings.Builder
	for _, m := range h {
		switch m.Role {
		case llm.RoleUser:
			b.WriteString("User: " + m.Text + "\n\n")
		case llm.RoleAssistant:
			if m.Text != "" {
				b.WriteString("Assistant: " + m.Text + "\n\n")
			}
			for _, tc := range m.ToolCalls {
				b.WriteString("Assistant used " + tc.Name + "(" + textutil.Truncate(tc.Args, 200) + ")\n")
			}
		case llm.RoleTool:
			b.WriteString("Result: " + textutil.Truncate(m.Text, 400) + "\n\n")
		case llm.RoleSystem:
			b.WriteString("Context: " + textutil.Truncate(m.Text, 400) + "\n\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// permissionMode maps werkz's mode to Claude Code's. werkz's auto mode still goes through the
// bridge, where werkz's own rules decide (writes outside the working folders always ask).
func permissionMode(mode string) string {
	switch mode {
	case "plan":
		return "plan"
	case "acceptEdits":
		return "acceptEdits"
	}
	return "default"
}

func (c *Client) run(ctx context.Context, req llm.Request, h llm.Handlers, id string, resume bool, prompt string) (*llm.Turn, error) {
	tmp, err := os.MkdirTemp("", "werkz-cc-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	sock := filepath.Join(tmp, "perm.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return nil, fmt.Errorf("can't start the permission bridge: %w", err)
	}
	defer ln.Close()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go serveApprovals(runCtx, ln, h)

	mcp, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{
		"werkz": map[string]any{"type": "stdio", "command": c.Exe, "args": []string{BridgeArg, sock}},
	}})
	mcpFile := filepath.Join(tmp, "mcp.json")
	if err := os.WriteFile(mcpFile, mcp, 0o600); err != nil {
		return nil, err
	}

	args := []string{"-p", "--output-format", "stream-json", "--verbose", "--include-partial-messages",
		"--permission-mode", permissionMode(req.Mode),
		"--permission-prompt-tool", "mcp__werkz__approve", "--mcp-config", mcpFile}
	if resume {
		args = append(args, "--resume", id)
	} else {
		args = append(args, "--session-id", id)
	}
	if req.Model != "" && req.Model != "default" {
		args = append(args, "--model", req.Model)
	}
	if req.Effort != "" && req.Effort != "auto" {
		args = append(args, "--effort", req.Effort)
	}
	if req.Window > 0 {
		// Claude Code's own compaction window, for this run only (its minimum is 100K)
		args = append(args, "--autocompact", strconv.Itoa(max(req.Window, 100_000)))
	}
	for _, d := range req.Dirs[min(1, len(req.Dirs)):] {
		args = append(args, "--add-dir", d)
	}

	cmd := exec.CommandContext(runCtx, c.Bin, args...)
	if len(req.Dirs) > 0 {
		cmd.Dir = req.Dirs[0]
	}
	cmd.Env = childEnv()
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &limitWriter{w: &stderr, n: 16 << 10}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("can't start Claude Code: %w", err)
	}
	turn, perr := parse(out, h)
	werr := cmd.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if perr != nil {
		return nil, perr
	}
	if turn == nil {
		msg := strings.TrimSpace(stderr.String())
		if resume && strings.Contains(strings.ToLower(msg), "no conversation found") {
			return nil, errNoSession
		}
		if msg == "" && werr != nil {
			msg = werr.Error()
		}
		return nil, &llm.HTTPError{Status: 502, Reason: "Claude Code failed: " + textutil.Truncate(firstLine(msg), 200)}
	}
	return turn, nil
}

// childEnv is werkz's environment without ANTHROPIC_API_KEY: with it set, `claude -p` bills
// that API key instead of the user's subscription.
func childEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") {
			env = append(env, kv)
		}
	}
	return env
}

// ccEvent is one line of `claude -p --output-format stream-json`.
type ccEvent struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	SessionID string          `json:"session_id"`
	Event     json.RawMessage `json:"event"`
	Message   *struct {
		Content []block `json:"content"`
		Usage   *usage  `json:"usage"`
	} `json:"message"`
	IsError bool    `json:"is_error"`
	Result  string  `json:"result"`
	Usage   *usage  `json:"usage"`
	Errors  []any   `json:"errors"`
	Cost    float64 `json:"total_cost_usd"`
}

type usage struct {
	In         int64 `json:"input_tokens"`
	Out        int64 `json:"output_tokens"`
	CacheRead  int64 `json:"cache_read_input_tokens"`
	CacheWrite int64 `json:"cache_creation_input_tokens"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

// streamEvent is the part of an Anthropic streaming event werkz reads.
type streamEvent struct {
	Type         string `json:"type"`
	ContentBlock struct {
		Type string `json:"type"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Thinking string `json:"thinking"`
	} `json:"delta"`
}

func parse(r io.Reader, h llm.Handlers) (*llm.Turn, error) {
	var text strings.Builder
	streamed := false // text arrived as deltas for the current message
	names := map[string]string{}
	var last *usage
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		var ev ccEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "stream_event":
			var se streamEvent
			if json.Unmarshal(ev.Event, &se) != nil {
				continue
			}
			switch se.Type {
			case "message_start":
				streamed = false
			case "content_block_start":
				switch se.ContentBlock.Type {
				case "tool_use":
					if h.OnToolStart != nil {
						h.OnToolStart(se.ContentBlock.Name)
					}
				case "text":
					if s := text.String(); s != "" && !strings.HasSuffix(s, "\n\n") {
						sep := "\n\n"
						if strings.HasSuffix(s, "\n") {
							sep = "\n"
						}
						text.WriteString(sep)
						emitText(h, sep)
					}
				}
			case "content_block_delta":
				switch se.Delta.Type {
				case "text_delta":
					streamed = true
					text.WriteString(se.Delta.Text)
					emitText(h, se.Delta.Text)
				case "thinking_delta":
					if h.OnThinking != nil {
						h.OnThinking(se.Delta.Thinking)
					}
				}
			}
		case "assistant":
			if ev.Message == nil {
				continue
			}
			if ev.Message.Usage != nil {
				last = ev.Message.Usage
			}
			for _, b := range ev.Message.Content {
				switch b.Type {
				case "text":
					if !streamed && b.Text != "" { // no partial messages: show the whole block
						if text.Len() > 0 {
							text.WriteString("\n\n")
							emitText(h, "\n\n")
						}
						text.WriteString(b.Text)
						emitText(h, b.Text)
					}
				case "tool_use":
					names[b.ID] = b.Name
					if h.OnExternalTool != nil {
						h.OnExternalTool(llm.ToolCall{ID: b.ID, Name: b.Name, Args: NormalizeArgs(b.Input)})
					}
				}
			}
		case "user":
			if ev.Message == nil {
				continue
			}
			for _, b := range ev.Message.Content {
				if b.Type == "tool_result" && h.OnExternalToolResult != nil {
					h.OnExternalToolResult(b.ToolUseID, names[b.ToolUseID], b.IsError, resultText(b.Content))
				}
			}
		case "result":
			if ev.IsError || strings.HasPrefix(ev.Subtype, "error") {
				return nil, resultError(ev)
			}
			turn := &llm.Turn{Text: text.String(), StopReason: "end_turn"}
			if turn.Text == "" {
				turn.Text = ev.Result
			}
			if last != nil { // the last call's size is how full the context is
				turn.Usage = llm.Usage{In: last.In, Out: last.Out, CacheRead: last.CacheRead, CacheWrite: last.CacheWrite}
			}
			if ev.Usage != nil {
				turn.Usage.Out = ev.Usage.Out
			}
			return turn, nil
		}
	}
	return nil, sc.Err()
}

func emitText(h llm.Handlers, s string) {
	if h.OnText != nil && s != "" {
		h.OnText(s)
	}
}

func resultError(ev ccEvent) error {
	msg := strings.TrimSpace(ev.Result)
	if msg == "" && len(ev.Errors) > 0 {
		msg = fmt.Sprint(ev.Errors[0])
	}
	low := strings.ToLower(msg)
	he := &llm.HTTPError{Status: 502, Body: msg}
	switch {
	case strings.Contains(low, "/login") || strings.Contains(low, "not logged in") || strings.Contains(low, "invalid api key") ||
		strings.Contains(low, "authenticate") || strings.Contains(low, "oauth"):
		he.Status, he.Reason, he.Fatal = 401, "Claude Code isn't signed in: run `claude` in a terminal and use /login", true
	case strings.Contains(low, "usage limit") || strings.Contains(low, "limit reached") || strings.Contains(low, "rate limit"):
		he.Status, he.Reason = 429, "Claude subscription limit reached: "+textutil.Truncate(firstLine(msg), 160)
	case ev.Subtype == "error_max_turns":
		he.Reason = "Claude Code stopped: too many steps in one turn"
	default:
		if msg == "" {
			msg = ev.Subtype
		}
		he.Reason = "Claude Code: " + textutil.Truncate(firstLine(msg), 200)
	}
	return he
}

func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &parts)
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// NormalizeArgs maps Claude Code's argument names onto werkz's (file_path → path), so werkz's
// summaries, previews and folder checks work on Claude Code's tool calls.
func NormalizeArgs(raw json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return string(raw)
	}
	for _, k := range []string{"file_path", "notebook_path"} {
		if v, ok := m[k]; ok {
			if _, has := m["path"]; !has {
				m["path"] = v
			}
		}
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

type limitWriter struct {
	w io.Writer
	n int
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.n > 0 {
		k := min(len(p), l.n)
		l.w.Write(p[:k]) //nolint:errcheck // a bytes.Buffer
		l.n -= k
	}
	return len(p), nil
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	x := hex.EncodeToString(b)
	return x[0:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:]
}
