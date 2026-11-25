// Package llm defines the provider-neutral conversation types (messages, tool calls,
// usage) and the Provider interface every model backend implements.
package llm

import (
	"context"

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
	// KeepAlive (Claude only) refreshes the prompt cache for this request's prefix.
	KeepAlive func(ctx context.Context) (Usage, error)
	// Note is something the user should know about how the answer was produced
	// (e.g. the model can't use tools, so it answered without them).
	Note string
}

type Handlers struct {
	OnText      func(string)
	OnThinking  func(string)
	OnToolStart func(name string)
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
}

type Provider interface {
	Stream(ctx context.Context, req Request, h Handlers) (*Turn, error)
}
