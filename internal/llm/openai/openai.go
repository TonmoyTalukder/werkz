// Package openai is the backend for OpenAI-compatible chat-completions APIs
// (OpenAI, xAI, Gemini, OpenRouter, Groq, Ollama).
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/catalog"
	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/llm"
)

// OpenAICompat talks to any OpenAI-style /chat/completions endpoint
// (OpenAI, xAI Grok, Gemini, OpenRouter, Groq, Ollama).
type Client struct {
	ID, BaseURL, Key string
	IncludePaid      bool // OpenRouter: list paid models too (default: only free ones)

	mu       sync.Mutex
	noTools  map[string]bool // models that rejected tool definitions this session
	noEffort map[string]bool // models that rejected reasoning_effort
}

// httpClient has connection and response-header timeouts (the default client has none) but no
// overall timeout, since a streamed answer can legitimately take minutes.
var httpClient = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	TLSHandshakeTimeout:   15 * time.Second,
	ResponseHeaderTimeout: 3 * time.Minute,
	IdleConnTimeout:       90 * time.Second,
	MaxIdleConnsPerHost:   4,
	ForceAttemptHTTP2:     true,
}}

type oaToolCall struct {
	Index    *int   `json:"index,omitempty"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaMsg struct {
	Role       string       `json:"role"`
	Content    *string      `json:"content"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

func strp(s string) *string { return &s }

func toOAMessages(system string, h []llm.Msg) []oaMsg {
	out := []oaMsg{{Role: "system", Content: strp(system)}}
	for _, m := range h {
		switch m.Role {
		case llm.RoleSystem:
			// keep the leading system prompt identical (OpenAI-style automatic prefix caching)
			out = append(out, oaMsg{Role: "user", Content: strp("<system-reminder>\n" + m.Text + "\n</system-reminder>")})
		case llm.RoleUser:
			out = append(out, oaMsg{Role: "user", Content: strp(m.Text)})
		case llm.RoleAssistant:
			om := oaMsg{Role: "assistant", Content: strp(m.Text)}
			for _, tc := range m.ToolCalls {
				c := oaToolCall{ID: tc.ID, Type: "function"}
				c.Function.Name = tc.Name
				c.Function.Arguments = tc.Args
				om.ToolCalls = append(om.ToolCalls, c)
			}
			if m.Text == "" && len(om.ToolCalls) == 0 {
				continue
			}
			out = append(out, om)
		case llm.RoleTool:
			out = append(out, oaMsg{Role: "tool", ToolCallID: m.ToolCallID, Content: strp(m.Text)})
		}
	}
	return out
}

func oaTools(defs []llm.ToolDef) []map[string]any {
	var out []map[string]any
	for _, d := range defs {
		// Strict providers (e.g. Baidu via OpenRouter) reject "required": null, so always send a list.
		req := d.Required
		if req == nil {
			req = []string{}
		}
		out = append(out, map[string]any{"type": "function", "function": map[string]any{
			"name": d.Name, "description": d.Description,
			"parameters": schemaFor(d, req),
		}})
	}
	return out
}

func (o *Client) Stream(ctx context.Context, req llm.Request, h llm.Handlers) (*llm.Turn, error) {
	body := map[string]any{
		"model":          req.Model,
		"messages":       toOAMessages(req.System, req.History),
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	noTools, noEffort := o.rejected(req.Model)
	if len(req.Tools) > 0 && !noTools {
		body["tools"] = oaTools(req.Tools)
	}
	if req.Effort != "" && req.Effort != "auto" && !noEffort {
		body["reasoning_effort"] = req.Effort
	}
	if o.ID == "openai" && req.CacheKey != "" {
		body["prompt_cache_key"] = req.CacheKey // keeps a session's requests on the same prefix cache
	}
	if o.ID == "openrouter" {
		body["usage"] = map[string]any{"include": true} // report the actual cost of each request
	}
	turn, err := o.do(ctx, body, h)
	var he *llm.HTTPError
	if errors.As(err, &he) && he.Status == 400 {
		// Some endpoints/models reject optional fields: retry once with a minimal request, and
		// remember what this model rejected so later requests don't pay for a failure first.
		low := strings.ToLower(he.Body)
		_, hadEffort := body["reasoning_effort"]
		_, hadTools := body["tools"]
		dropTools := hadTools && strings.Contains(low, "tool")
		delete(body, "reasoning_effort")
		delete(body, "stream_options")
		delete(body, "prompt_cache_key")
		delete(body, "usage")
		if dropTools {
			delete(body, "tools")
		}
		turn, err = o.do(ctx, body, h)
		if err == nil {
			o.remember(req.Model, dropTools, hadEffort && strings.Contains(low, "reasoning"))
			if dropTools {
				turn.Note = req.Model + " can't use tools, so it answered without reading or changing files; pick a tool-capable model with /model for coding work"
			}
		}
	}
	return turn, err
}

// rejected reports what this model refused earlier this session.
func (o *Client) rejected(model string) (tools, effort bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.noTools[model], o.noEffort[model]
}

func (o *Client) remember(model string, tools, effort bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.noTools == nil {
		o.noTools, o.noEffort = map[string]bool{}, map[string]bool{}
	}
	if tools {
		o.noTools[model] = true
	}
	if effort {
		o.noEffort[model] = true
	}
}

func (o *Client) newReq(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	r, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(o.BaseURL, "/")+path, body)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	if o.Key != "" && o.ID != "ollama" {
		r.Header.Set("Authorization", "Bearer "+o.Key)
	}
	if o.ID == "openrouter" {
		r.Header.Set("X-Title", "werkz")
	}
	return r, nil
}

func (o *Client) do(ctx context.Context, body map[string]any, h llm.Handlers) (*llm.Turn, error) {
	b, _ := json.Marshal(body)
	r, err := o.newReq(ctx, "POST", "/chat/completions", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(r)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		eb, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &llm.HTTPError{Status: resp.StatusCode, Body: string(eb)}
	}

	turn := &llm.Turn{}
	calls := map[int]*llm.ToolCall{}
	lastIdx, done := -1, false
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			done = true
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string       `json:"content"`
					Reasoning        string       `json:"reasoning"`
					ReasoningContent string       `json:"reasoning_content"`
					ToolCalls        []oaToolCall `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				Prompt     int64   `json:"prompt_tokens"`
				Completion int64   `json:"completion_tokens"`
				Cost       float64 `json:"cost"` // OpenRouter: actual USD charged
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		if chunk.Error != nil {
			return nil, &llm.HTTPError{Status: 502, Body: chunk.Error.Message}
		}
		if chunk.Usage != nil {
			turn.Usage = llm.Usage{In: chunk.Usage.Prompt, Out: chunk.Usage.Completion, CostUSD: chunk.Usage.Cost}
		}
		for _, ch := range chunk.Choices {
			d := ch.Delta
			if d.Content != "" {
				turn.Text += d.Content
				h.OnText(d.Content)
			}
			if t := d.Reasoning + d.ReasoningContent; t != "" {
				h.OnThinking(t)
			}
			for _, tc := range d.ToolCalls {
				// Providers that omit "index" stream one call after another: a new id starts a
				// new call, chunks without an id continue the current one.
				idx := lastIdx
				switch {
				case tc.Index != nil:
					idx = *tc.Index
				case tc.ID != "" && (idx < 0 || calls[idx].ID != "" && calls[idx].ID != tc.ID):
					idx = len(calls)
				case idx < 0:
					idx = 0
				}
				lastIdx = idx
				c, ok := calls[idx]
				if !ok {
					c = &llm.ToolCall{}
					calls[idx] = c
				}
				if tc.ID != "" {
					c.ID = tc.ID
				}
				if tc.Function.Name != "" {
					if c.Name == "" {
						h.OnToolStart(tc.Function.Name)
					}
					c.Name = tc.Function.Name
				}
				c.Args += tc.Function.Arguments
			}
			if ch.FinishReason != "" {
				turn.StopReason = ch.FinishReason
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if !done && turn.StopReason == "" {
		return nil, &llm.HTTPError{Status: 502, Body: "the response stream from " + o.ID + " ended early (connection dropped?)"}
	}
	keys := make([]int, 0, len(calls))
	for k := range calls {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		c := calls[k]
		if c.ID == "" {
			c.ID = "call_" + strconv.Itoa(k) + "_" + strconv.FormatInt(time.Now().UnixNano(), 36)
		}
		if strings.TrimSpace(c.Args) == "" {
			c.Args = "{}"
		}
		turn.ToolCalls = append(turn.ToolCalls, *c)
	}
	if turn.Text == "" && len(turn.ToolCalls) == 0 {
		return nil, &llm.HTTPError{Status: 502, Body: "empty response from " + o.ID}
	}
	return turn, nil
}

// ListModels fetches the provider's model list (OpenAI-compatible /models).
func (o *Client) ListModels(ctx context.Context) ([]catalog.ModelInfo, error) {
	r, err := o.newReq(ctx, "GET", "/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(r)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		eb, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, &llm.HTTPError{Status: resp.StatusCode, Body: string(eb)}
	}
	var lr struct {
		Data []struct {
			ID            string     `json:"id"`
			ContextLength int        `json:"context_length"`
			Pricing       *orPricing `json:"pricing"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return nil, err
	}
	p := config.ProviderByID(o.ID)
	var out []catalog.ModelInfo
	for _, d := range lr.Data {
		id := strings.TrimPrefix(d.ID, "models/")
		low := strings.ToLower(id)
		if strings.Contains(low, "embed") || strings.Contains(low, "tts") || strings.Contains(low, "whisper") ||
			strings.Contains(low, "dall-e") || strings.Contains(low, "image") || strings.Contains(low, "moderation") ||
			strings.Contains(low, "audio") || strings.Contains(low, "transcribe") {
			continue
		}
		mi := catalog.ModelInfo{ID: id, Label: id, Provider: o.ID, Context: 128_000, Efforts: catalog.OpenAIEfforts, DefaultEffort: "auto"}
		if d.ContextLength > 0 {
			mi.Context = d.ContextLength
		}
		if o.ID == "openrouter" {
			pin, pout := d.Pricing.prices()
			in, outp := perMillion(pin, pout)
			mi.Free = pin == "0" && pout == "0" // exactly free; "-1" means variable price, not free
			if !mi.Free && !o.IncludePaid {
				continue // free models only unless paid OpenRouter models are enabled
			}
			mi.In, mi.Out = in, outp
		} else if p != nil && p.Free {
			mi.Free = true
		}
		out = append(out, mi)
	}
	// free models first, then by name
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Free != out[j].Free {
			return out[i].Free
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

type orPricing struct {
	Prompt     string `json:"prompt"`
	Completion string `json:"completion"`
}

func (p *orPricing) prices() (string, string) {
	if p == nil {
		return "", ""
	}
	return p.Prompt, p.Completion
}

// perMillion converts OpenRouter's per-token USD price strings to USD per 1M tokens.
// Missing or negative values (OpenRouter uses -1 for variable-price router models) count as 0.
func perMillion(prompt, completion string) (in, out float64) {
	conv := func(s string) float64 {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil || v < 0 {
			return 0
		}
		return v * 1e6
	}
	return conv(prompt), conv(completion)
}

// OpenRouterCredits returns remaining credits (total - usage) when the endpoint is available.
func OpenRouterCredits(ctx context.Context, key string) (float64, bool) {
	o := &Client{ID: "openrouter", BaseURL: "https://openrouter.ai/api/v1", Key: key}
	r, err := o.newReq(ctx, "GET", "/credits", nil)
	if err != nil {
		return 0, false
	}
	resp, err := httpClient.Do(r)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	var cr struct {
		Data struct {
			Total float64 `json:"total_credits"`
			Usage float64 `json:"total_usage"`
		} `json:"data"`
	}
	if resp.StatusCode >= 300 || json.NewDecoder(resp.Body).Decode(&cr) != nil {
		return 0, false
	}
	return cr.Data.Total - cr.Data.Usage, true
}

// schemaFor is a tool's input schema: werkz's properties and required list, plus any other
// top-level keys an MCP server gave ($defs, additionalProperties…).
func schemaFor(d llm.ToolDef, req []string) map[string]any {
	s := map[string]any{"type": "object", "properties": d.Properties, "required": req}
	for k, v := range d.Extra {
		if _, taken := s[k]; !taken {
			s[k] = v
		}
	}
	return s
}
