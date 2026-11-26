package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/llm"
	"github.com/TonmoyTalukder/werkz/internal/mcp"
	"github.com/TonmoyTalukder/werkz/internal/textutil"
	"github.com/TonmoyTalukder/werkz/internal/tools"
	"github.com/TonmoyTalukder/werkz/internal/web"
)

// MCP: tools from the user's MCP servers appear to the model as mcp__<server>__<tool>. Each one
// asks the first time it is used (with "don't ask again" for that tool), except in auto and
// bypass mode; in plan mode only tools the server marks read-only may run. What a tool returns
// comes from another program, so it reaches the model marked as untrusted.

const mcpCallTimeout = 2 * time.Minute

// SetMCP connects the engine to the session's MCP servers.
func (e *Engine) SetMCP(m *mcp.Manager) { e.mcp = m }

func isMCPTool(name string) bool { return strings.HasPrefix(name, "mcp__") }

// mcpDefs are the MCP tools as tool definitions, in a stable order (so the cached prefix holds
// while the set doesn't change).
func mcpDefs(list []mcp.Tool) []llm.ToolDef {
	sort.Slice(list, func(i, j int) bool { return list[i].FullName < list[j].FullName })
	out := make([]llm.ToolDef, 0, len(list))
	for _, t := range list {
		d := llm.ToolDef{Name: t.FullName, Description: mcpDescription(t), Properties: map[string]any{}}
		for k, v := range t.Schema {
			switch k {
			case "type":
			case "properties":
				if p, ok := v.(map[string]any); ok {
					d.Properties = p
				}
			case "required":
				if r, ok := v.([]any); ok {
					for _, x := range r {
						if s, ok := x.(string); ok {
							d.Required = append(d.Required, s)
						}
					}
				}
			default:
				if d.Extra == nil {
					d.Extra = map[string]any{}
				}
				d.Extra[k] = v
			}
		}
		out = append(out, d)
	}
	return out
}

func mcpDescription(t mcp.Tool) string {
	desc := strings.TrimSpace(t.Description)
	if r := []rune(desc); len(r) > 2000 {
		desc = string(r[:1999]) + "…"
	}
	return "[MCP server " + t.Server + "] " + desc
}

// mcpCheck: the rules for an MCP tool call. need/kind/key/label as in permit.
func (e *Engine) mcpCheck(tc llm.ToolCall) (need bool, kind, key, label string, refuse error) {
	if e.mcp == nil {
		return false, "", "", "", fmt.Errorf("%s isn't available: no MCP servers are connected", tc.Name)
	}
	t, ok := e.mcp.Lookup(tc.Name)
	if !ok {
		return false, "", "", "", fmt.Errorf("%s isn't available now (its MCP server disconnected); see /mcp", tc.Name)
	}
	switch e.mode() {
	case config.ModeAuto, config.ModeBypass:
		return false, "", "", "", nil
	case config.ModePlan:
		if !t.ReadOnly {
			return true, "plan", "", "", nil
		}
	}
	return true, "ask", "mcp:" + tc.Name, "Yes, and don't ask again for " + t.Server + "'s " + t.Name + " this session", nil
}

// mcpSummary is the one-line description of an MCP call: its arguments (the UI shows the tool as
// "server · tool").
func mcpSummary(tc llm.ToolCall) string {
	args := strings.Join(strings.Fields(tc.Args), " ")
	if args == "{}" {
		args = ""
	}
	return textutil.Truncate(args, 100)
}

// DisplayName is how the UI names a tool: MCP tools as "server · tool".
func DisplayName(name string) string {
	if !isMCPTool(name) {
		return name
	}
	server, tool := splitMCPName(name)
	return server + " · " + tool
}

func splitMCPName(full string) (server, tool string) {
	rest := strings.TrimPrefix(full, "mcp__")
	server, tool, _ = strings.Cut(rest, "__")
	return server, tool
}

// mcpPreview shows the arguments in the permission dialog, one per line.
func mcpPreview(tc llm.ToolCall) []string {
	var args map[string]any
	if json.Unmarshal([]byte(tc.Args), &args) != nil || len(args) == 0 {
		return nil
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		b, _ := json.Marshal(args[k])
		out = append(out, k+": "+textutil.Truncate(string(b), 200))
	}
	return out
}

func (e *Engine) runMCP(ctx context.Context, tc llm.ToolCall) tools.Result {
	ctx, cancel := context.WithTimeout(ctx, mcpCallTimeout)
	defer cancel()
	args := json.RawMessage(tc.Args)
	if len(strings.TrimSpace(tc.Args)) == 0 {
		args = json.RawMessage("{}")
	}
	res, err := e.mcp.Call(ctx, tc.Name, args)
	if err != nil {
		return tools.Fail("%v", err)
	}
	server, tool := splitMCPName(tc.Name)
	out := tools.Result{Output: web.Untrusted("mcp", server+"/"+tool, res.Text), IsError: res.IsError}
	lines := strings.Count(strings.TrimSpace(res.Text), "\n") + 1
	if strings.TrimSpace(res.Text) == "" {
		lines = 0
	}
	out.Summary = fmt.Sprintf("%d line%s", lines, map[bool]string{true: "", false: "s"}[lines == 1])
	if res.IsError {
		first, _, _ := strings.Cut(strings.TrimSpace(res.Text), "\n")
		out.Summary = "error: " + textutil.Truncate(first, 80)
	}
	if len(res.Images) > 0 {
		if p := saveMCPImage(res.Images[0]); p != "" {
			out.Image = p
			out.Output += fmt.Sprintf("\n(The tool returned %d image(s); the first was saved to %s.)", len(res.Images), p)
		}
	}
	return out
}

func saveMCPImage(img mcp.Image) string {
	ext := map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}[img.MIME]
	if ext == "" {
		return ""
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	dir = filepath.Join(dir, "werkz", "mcp-images")
	if os.MkdirAll(dir, 0o700) != nil {
		return ""
	}
	p := filepath.Join(dir, time.Now().Format("20060102-150405.000")+ext)
	if os.WriteFile(p, img.Data, 0o600) != nil {
		return ""
	}
	return p
}
