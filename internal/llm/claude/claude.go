// Package claude is the Anthropic Messages API backend: streaming, adaptive thinking,
// effort, fast mode, prompt caching and cache keep-alive.
package claude

import (
	"context"
	"encoding/json"
	"regexp"

	"github.com/TonmoyTalukder/werkz/internal/catalog"
	"github.com/TonmoyTalukder/werkz/internal/llm"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

type Client struct{ client anthropic.Client }

// New returns a Claude client; baseURL overrides the API endpoint ("" = default).
func New(key, baseURL string) *Client {
	// One SDK retry, then werkz's own provider fallback takes over.
	opts := []option.RequestOption{option.WithAPIKey(key), option.WithMaxRetries(1)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &Client{client: anthropic.NewClient(opts...)}
}

var badIDChars = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func claudeID(id string) string {
	if id == "" {
		return "toolu_x"
	}
	return badIDChars.ReplaceAllString(id, "_")
}

// toClaudeMessages converts neutral history. midSystem: the model accepts role:"system"
// messages inside messages[] (keeps the cached prefix intact when context changes).
func ToMessages(h []llm.Msg, midSystem bool) []anthropic.BetaMessageParam {
	var out []anthropic.BetaMessageParam
	for i := 0; i < len(h); i++ {
		m := h[i]
		switch m.Role {
		case llm.RoleSystem:
			// Valid only after a user turn and when last or followed by an assistant turn;
			// otherwise send it as a user text block (same caching profile).
			prevUser := len(out) > 0 && out[len(out)-1].OfUser != nil
			nextOK := i == len(h)-1 || h[i+1].Role == llm.RoleAssistant
			if midSystem && prevUser && nextOK {
				tb := anthropic.BetaTextBlockParam{Text: m.Text}
				out = append(out, anthropic.NewBetaSystemMessage(anthropic.BetaSystemMessageOutputConfigParam{},
					anthropic.BetaSystemMessageParamContentUnion{OfText: &tb}))
			} else {
				out = append(out, anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock("<system-reminder>\n"+m.Text+"\n</system-reminder>")))
			}
		case llm.RoleUser:
			out = append(out, anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(m.Text)))
		case llm.RoleAssistant:
			if m.ClaudeRaw != nil {
				out = append(out, *m.ClaudeRaw)
				continue
			}
			var blocks []anthropic.BetaContentBlockParamUnion
			if m.Text != "" {
				blocks = append(blocks, anthropic.NewBetaTextBlock(m.Text))
			}
			for _, tc := range m.ToolCalls {
				var in any
				_ = json.Unmarshal([]byte(tc.Args), &in)
				if in == nil {
					in = map[string]any{}
				}
				blocks = append(blocks, anthropic.NewBetaToolUseBlock(claudeID(tc.ID), in, tc.Name))
			}
			if len(blocks) > 0 {
				out = append(out, anthropic.BetaMessageParam{OfAssistant: &anthropic.BetaAssistantMessageParam{Content: blocks}})
			}
		case llm.RoleTool:
			var blocks []anthropic.BetaContentBlockParamUnion
			for ; i < len(h) && h[i].Role == llm.RoleTool; i++ {
				blocks = append(blocks, anthropic.NewBetaToolResultBlock(claudeID(h[i].ToolCallID), h[i].Text, h[i].IsError))
			}
			i--
			out = append(out, anthropic.NewBetaUserMessage(blocks...))
		}
	}
	return out
}

func claudeTools(defs []llm.ToolDef) []anthropic.BetaToolUnionParam {
	var out []anthropic.BetaToolUnionParam
	for _, d := range defs {
		t := anthropic.BetaToolParam{
			Name:        d.Name,
			Description: anthropic.String(d.Description),
			InputSchema: anthropic.BetaToolInputSchemaParam{
				Properties: d.Properties,
				Required:   d.Required,
			},
		}
		out = append(out, anthropic.BetaToolUnionParam{OfTool: &t})
	}
	return out
}

func (c *Client) Stream(ctx context.Context, req llm.Request, h llm.Handlers) (*llm.Turn, error) {
	mi := catalog.Lookup("anthropic", req.Model)
	// Prompt caching: an explicit breakpoint on the frozen system prompt caches tools+system
	// (render order is tools → system → messages) and survives anything later in the
	// conversation; the top-level automatic breakpoint caches the growing conversation tail.
	cc := anthropic.NewBetaCacheControlEphemeralParam()
	if req.TTL1h {
		cc.TTL = anthropic.BetaCacheControlEphemeralTTLTTL1h
	}
	params := anthropic.BetaMessageNewParams{
		Model:        anthropic.Model(req.Model),
		MaxTokens:    64000,
		System:       []anthropic.BetaTextBlockParam{{Text: req.System, CacheControl: cc}},
		Messages:     ToMessages(req.History, mi.MidSystem),
		Tools:        claudeTools(req.Tools),
		CacheControl: cc,
		Thinking: anthropic.BetaThinkingConfigParamUnion{OfAdaptive: &anthropic.BetaThinkingConfigAdaptiveParam{
			Display: anthropic.BetaThinkingConfigAdaptiveDisplaySummarized,
		}},
	}
	if req.Effort != "" && req.Effort != "auto" {
		params.OutputConfig = anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffort(req.Effort)}
	}
	if req.Fast && mi.FastIn > 0 {
		params.Speed = anthropic.BetaMessageNewParamsSpeedFast
	}
	if mi.ServerFallback {
		// Server-side refusal fallback: a policy decline is re-served by a fallback model in the same call.
		params.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
		params.Betas = append(params.Betas, anthropic.AnthropicBetaServerSideFallback2026_07_01)
	}

	stream := c.client.Beta.Messages.NewStreaming(ctx, params)
	msg := anthropic.BetaMessage{}
	for stream.Next() {
		ev := stream.Current()
		if err := msg.Accumulate(ev); err != nil {
			return nil, err
		}
		switch e := ev.AsAny().(type) {
		case anthropic.BetaRawContentBlockStartEvent:
			if e.ContentBlock.Type == "tool_use" {
				h.OnToolStart(e.ContentBlock.Name)
			}
		case anthropic.BetaRawContentBlockDeltaEvent:
			switch d := e.Delta.AsAny().(type) {
			case anthropic.BetaTextDelta:
				h.OnText(d.Text)
			case anthropic.BetaThinkingDelta:
				h.OnThinking(d.Thinking)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return nil, err
	}

	turn := &llm.Turn{StopReason: string(msg.StopReason)}
	turn.Usage = llm.Usage{
		In: msg.Usage.InputTokens, Out: msg.Usage.OutputTokens,
		CacheRead: msg.Usage.CacheReadInputTokens, CacheWrite: msg.Usage.CacheCreationInputTokens,
	}
	for _, b := range msg.Content {
		switch v := b.AsAny().(type) {
		case anthropic.BetaTextBlock:
			turn.Text += v.Text
		case anthropic.BetaToolUseBlock:
			args, _ := json.Marshal(v.Input)
			turn.ToolCalls = append(turn.ToolCalls, llm.ToolCall{ID: v.ID, Name: v.Name, Args: string(args)})
		}
	}
	raw := msg.ToParam()
	turn.Raw = &raw

	// Keep-alive: re-send this exact prefix with max_tokens 0 (no streaming, no output billed)
	// to refresh the cache timer for the price of a cache read.
	ka := params
	ka.MaxTokens = 0
	ka.Fallbacks = anthropic.BetaFallbacksParamUnion{}
	ka.Betas = nil
	turn.KeepAlive = func(ctx context.Context) (llm.Usage, error) {
		r, err := c.client.Beta.Messages.New(ctx, ka)
		if err != nil {
			return llm.Usage{}, err
		}
		return llm.Usage{In: r.Usage.InputTokens, CacheRead: r.Usage.CacheReadInputTokens, CacheWrite: r.Usage.CacheCreationInputTokens}, nil
	}
	return turn, nil
}
