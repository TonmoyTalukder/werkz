package claudecode

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/llm"
)

// approval is a permission question from Claude Code (the arguments of its
// --permission-prompt-tool), as forwarded by the bridge.
type approval struct {
	ToolName  string          `json:"tool_name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
}

// decision is the reply Claude Code expects from a permission prompt tool.
type decision struct {
	Behavior     string          `json:"behavior"` // allow | deny
	UpdatedInput json.RawMessage `json:"updatedInput,omitempty"`
	Message      string          `json:"message,omitempty"`
}

// toolPrefix is how Claude Code names the tools werkz's bridge offers.
const toolPrefix = "mcp__werkz__"

// request is one message from the bridge: a permission question, or (Call set) a call of one of
// werkz's own tools.
type request struct {
	approval
	Call *toolCall `json:"call,omitempty"`
}

type toolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// toolReply is werkz's answer to a tool call.
type toolReply struct {
	Text    string `json:"text"`
	IsError bool   `json:"isError,omitempty"`
	Image   string `json:"image,omitempty"` // base64
	MIME    string `json:"mime,omitempty"`
}

// serveApprovals answers the bridge: permission questions with werkz's rules and dialog, and
// calls of werkz's own tools (hosted) by running them in werkz.
func serveApprovals(ctx context.Context, ln net.Listener, h llm.Handlers, hosted map[string]bool) {
	var calls int64
	var mu sync.Mutex
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			var r request
			if err := json.NewDecoder(conn).Decode(&r); err != nil {
				return
			}
			if r.Call != nil {
				mu.Lock()
				calls++
				id := fmt.Sprintf("werkz-cc-%d", calls)
				mu.Unlock()
				_ = json.NewEncoder(conn).Encode(runHosted(ctx, h, hosted, id, *r.Call))
				return
			}
			a := r.approval
			d := decision{Behavior: "deny", Message: "werkz couldn't ask the user"}
			input := a.Input
			if len(input) == 0 {
				input = json.RawMessage("{}")
			}
			if hosted[strings.TrimPrefix(a.ToolName, toolPrefix)] && strings.HasPrefix(a.ToolName, toolPrefix) {
				// werkz's own tool: werkz applies its rules (and asks) when it runs it
				d = decision{Behavior: "allow", UpdatedInput: input}
			} else if h.Approve != nil {
				ok, reason := h.Approve(ctx, llm.ToolCall{ID: a.ToolUseID, Name: a.ToolName, Args: NormalizeArgs(input)})
				if ok {
					d = decision{Behavior: "allow", UpdatedInput: input}
				} else {
					d = decision{Behavior: "deny", Message: reason}
				}
			}
			_ = json.NewEncoder(conn).Encode(d)
		}()
	}
}

// runHosted runs one of werkz's own tools for Claude Code.
func runHosted(ctx context.Context, h llm.Handlers, hosted map[string]bool, id string, c toolCall) toolReply {
	if !hosted[c.Name] || h.RunTool == nil {
		return toolReply{Text: "werkz doesn't offer a tool named " + c.Name, IsError: true}
	}
	args := c.Arguments
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	out, isErr, image := h.RunTool(ctx, llm.ToolCall{ID: id, Name: c.Name, Args: string(args)})
	r := toolReply{Text: out, IsError: isErr}
	if image != "" {
		mime := map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp"}[strings.ToLower(filepath.Ext(image))]
		if b, err := os.ReadFile(image); err == nil && mime != "" && len(b) <= maxImage {
			r.Image, r.MIME = base64.StdEncoding.EncodeToString(b), mime
		}
	}
	return r
}

// maxImage is the largest picture passed back to Claude Code (its API's per-image limit is 5 MB).
const maxImage = 3 << 20

// Bridge is the MCP server Claude Code starts (werkz __claude-code-permissions <socket>
// [<tools file>]). It offers "approve", Claude Code's permission prompt, plus werkz's own tools
// listed in the tools file, and forwards every call to the werkz window that started Claude
// Code. It speaks MCP's stdio transport: one JSON-RPC message per line.
func Bridge(socket, toolsFile string, in io.Reader, out io.Writer) error {
	var hosted []any
	if toolsFile != "" {
		b, err := os.ReadFile(toolsFile)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(b, &hosted); err != nil {
			return err
		}
	}
	enc := json.NewEncoder(out)
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &msg) != nil || len(msg.ID) == 0 {
			continue // notifications need no reply
		}
		reply := map[string]any{"jsonrpc": "2.0", "id": msg.ID}
		switch msg.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			if p.ProtocolVersion == "" {
				p.ProtocolVersion = "2025-06-18"
			}
			reply["result"] = map[string]any{
				"protocolVersion": p.ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "werkz", "version": "1"},
			}
		case "ping":
			reply["result"] = map[string]any{}
		case "tools/list":
			reply["result"] = map[string]any{"tools": append([]any{map[string]any{
				"name":        "approve",
				"description": "Asks the werkz user whether Claude Code may use a tool.",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
					"tool_name":   map[string]any{"type": "string"},
					"input":       map[string]any{"type": "object"},
					"tool_use_id": map[string]any{"type": "string"},
				}, "required": []string{"tool_name", "input"}},
			}}, hosted...)}
		case "tools/call":
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			if p.Name != "approve" {
				reply["result"] = callHosted(socket, p.Name, p.Arguments)
				break
			}
			d := ask(socket, p.Arguments)
			text, _ := json.Marshal(d)
			reply["result"] = map[string]any{"content": []any{map[string]any{"type": "text", "text": string(text)}}}
		default:
			reply["error"] = map[string]any{"code": -32601, "message": "method not found"}
		}
		if err := enc.Encode(reply); err != nil {
			return err
		}
	}
	return sc.Err()
}

// ask forwards one permission question to werkz and waits for the user's answer.
func ask(socket string, args json.RawMessage) decision {
	deny := decision{Behavior: "deny", Message: "werkz isn't available to approve this"}
	conn, err := net.DialTimeout("unix", socket, 5*time.Second)
	if err != nil {
		return deny
	}
	defer conn.Close()
	if _, err := conn.Write(append(args, '\n')); err != nil {
		return deny
	}
	var d decision
	if json.NewDecoder(conn).Decode(&d) != nil || d.Behavior == "" {
		return deny
	}
	return d
}

// callHosted forwards a call of one of werkz's own tools and waits for its result (which may
// take as long as the user takes to answer werkz's permission dialog).
func callHosted(socket, name string, args json.RawMessage) map[string]any {
	fail := func(msg string) map[string]any {
		return map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": msg}}}
	}
	conn, err := net.DialTimeout("unix", socket, 5*time.Second)
	if err != nil {
		return fail("werkz isn't available to run " + name)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(request{Call: &toolCall{Name: name, Arguments: args}}); err != nil {
		return fail("werkz isn't available to run " + name)
	}
	var r toolReply
	if err := json.NewDecoder(conn).Decode(&r); err != nil {
		return fail("werkz stopped before " + name + " finished")
	}
	content := []any{map[string]any{"type": "text", "text": r.Text}}
	if r.Image != "" {
		content = append(content, map[string]any{"type": "image", "data": r.Image, "mimeType": r.MIME})
	}
	return map[string]any{"isError": r.IsError, "content": content}
}

// hostedTools are the MCP tool entries for werkz's own tools.
func hostedTools(defs []llm.ToolDef) []any {
	out := make([]any, 0, len(defs))
	for _, d := range defs {
		schema := map[string]any{"type": "object", "properties": d.Properties}
		if d.Properties == nil {
			schema["properties"] = map[string]any{}
		}
		if len(d.Required) > 0 {
			schema["required"] = d.Required
		}
		for k, v := range d.Extra {
			schema[k] = v
		}
		out = append(out, map[string]any{"name": d.Name, "description": d.Description, "inputSchema": schema})
	}
	return out
}
