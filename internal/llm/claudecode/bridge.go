package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
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

// serveApprovals answers the bridge's questions with werkz's permission rules and dialog.
func serveApprovals(ctx context.Context, ln net.Listener, h llm.Handlers) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			var a approval
			if err := json.NewDecoder(conn).Decode(&a); err != nil {
				return
			}
			d := decision{Behavior: "deny", Message: "werkz couldn't ask the user"}
			if h.Approve != nil {
				input := a.Input
				if len(input) == 0 {
					input = json.RawMessage("{}")
				}
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

// Bridge is the MCP server Claude Code starts (werkz __claude-code-permissions <socket>). It
// offers one tool, "approve", and forwards each call to the werkz window that started Claude
// Code. It speaks MCP's stdio transport: one JSON-RPC message per line.
func Bridge(socket string, in io.Reader, out io.Writer) error {
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
			reply["result"] = map[string]any{"tools": []any{map[string]any{
				"name":        "approve",
				"description": "Asks the werkz user whether Claude Code may use a tool.",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
					"tool_name":   map[string]any{"type": "string"},
					"input":       map[string]any{"type": "object"},
					"tool_use_id": map[string]any{"type": "string"},
				}, "required": []string{"tool_name", "input"}},
			}}}
		case "tools/call":
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			_ = json.Unmarshal(msg.Params, &p)
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
