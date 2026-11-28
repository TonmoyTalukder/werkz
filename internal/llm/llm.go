// Package llm defines the provider-neutral conversation types (messages, tool calls,
// usage) and the Provider interface every model backend implements.
package llm

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

// ToolCall is a provider-neutral tool invocation.
type ToolCall struct {
	ID   string
	Name string
	Args string // raw JSON
}

// Msg is the provider-neutral history entry, so a conversation can move between providers.
// Role is who a message is from.
type Role string

// Message roles.
const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"   // a tool result
	RoleSystem    Role = "system" // mid-conversation context, e.g. working folders
)

type Msg struct {
	Role       Role
	Text       string
	ToolCalls  []ToolCall
	ToolCallID string
	ToolName   string
	IsError    bool
	// Exact Claude assistant message (incl. thinking blocks) for faithful replay to Claude.
	ClaudeRaw *anthropic.BetaMessageParam
	// RespItems are the exact Responses API output items (incl. encrypted reasoning) for faithful
	// replay to the ChatGPT plan backend.
	RespItems json.RawMessage
	// Steps is what a backend that runs its own tools (Claude Code) did during this assistant
	// turn: each tool and a short part of its result. Other models see it with the answer
	// (WithSteps), so switching models mid-conversation keeps what was found.
	Steps string `json:",omitempty"`
}

// Steps of the latest stepsFull assistant messages are passed on whole; older ones are cut to
// stepsOld characters, so a long Claude Code session doesn't outgrow the next model's window.
const (
	stepsFull = 3
	stepsOld  = 1000
)

// WithSteps returns h with each assistant message's Steps written into its text, for a model
// that didn't run those steps itself. h is returned as is when no message has steps.
func WithSteps(h []Msg) []Msg {
	n := 0
	for _, m := range h {
		if m.Steps != "" && m.Role == RoleAssistant {
			n++
		}
	}
	if n == 0 {
		return h
	}
	out := make([]Msg, len(h))
	seen := 0
	for i, m := range h {
		if m.Steps != "" && m.Role == RoleAssistant {
			seen++
			steps := m.Steps
			if n-seen >= stepsFull && len(steps) > stepsOld {
				steps = strings.ToValidUTF8(steps[:stepsOld], "") + "\n- (rest of this turn's steps left out)"
			}
			text := "[Tools used in this turn by Claude Code, with the start of each result]\n" + steps
			if m.Text != "" {
				text += "\n\n" + m.Text
			}
			m.Text, m.Steps, m.ClaudeRaw, m.RespItems = text, "", nil, nil
		}
		out[i] = m
	}
	return out
}

type Usage struct {
	In, Out, CacheRead, CacheWrite int64
	// CostUSD is the cost the provider itself reported for the request (OpenRouter does);
	// 0 when not reported, in which case werkz estimates from the price table.
	CostUSD float64
}

type Turn struct {
	Text       string
	ToolCalls  []ToolCall
	Usage      Usage
	StopReason string
	Raw        *anthropic.BetaMessageParam
	Items      json.RawMessage // Responses API output items (see Msg.RespItems)
	// KeepAlive (Claude only) refreshes the prompt cache for this request's prefix.
	KeepAlive func(ctx context.Context) (Usage, error)
	// Note is something the user should know about how the answer was produced
	// (e.g. the model can't use tools, so it answered without them).
	Note string
	// Steps is what the backend's own tools did in this turn (see Msg.Steps).
	Steps string
}

type Handlers struct {
	OnText      func(string)
	OnThinking  func(string)
	OnToolStart func(name string)
	// External tools are run by the backend itself (Claude Code): werkz only shows them, and
	// is asked for permission through Approve.
	OnExternalTool       func(tc ToolCall)
	OnExternalToolResult func(id, name string, isError bool, output string)
	Approve              func(ctx context.Context, tc ToolCall) (allow bool, reason string)
	// RunTool runs one of werkz's own tools (Request.HostTools) for a backend that offers them
	// to its model: Claude Code reaches werkz's browser this way. image is a picture's path or "".
	RunTool func(ctx context.Context, tc ToolCall) (output string, isError bool, image string)
	// OnPlanLimits receives a subscription's usage limits when the backend reports them.
	OnPlanLimits func(PlanLimits)
}

type Request struct {
	Model    string
	System   string
	History  []Msg
	Tools    []ToolDef
	Effort   string
	Fast     bool
	TTL1h    bool   // 1-hour prompt cache instead of 5 minutes
	CacheKey string // stable per session: OpenAI prompt_cache_key
	// For backends that run their own tools (Claude Code): the working folders (first = main)
	// and werkz's permission mode.
	Dirs []string
	Mode string
	// Window is the user's context window limit in tokens (0 = the model's full window); Claude
	// Code receives it as its auto-compact window.
	Window int
	// HostTools are werkz's own tools that a backend running its own tools lacks (the browser,
	// DevServer, MCP servers' tools); it offers them to its model and runs them through RunTool.
	HostTools []ToolDef
}

type Provider interface {
	Stream(ctx context.Context, req Request, h Handlers) (*Turn, error)
}
