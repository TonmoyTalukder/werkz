// Package responses is the backend for OpenAI's Responses API used with a ChatGPT plan sign-in
// ("Sign in with ChatGPT"): requests are stateless (store: false, full history every time) and
// always streamed.
package responses

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/catalog"
	"github.com/TonmoyTalukder/werkz/internal/llm"
)

// ManageUsage is where users see and limit their plan usage.
const ManageUsage = "chatgpt.com/settings/usage"

// Client calls POST {BaseURL}/responses with a ChatGPT plan access token.
type Client struct {
	ID      string // provider ID, "chatgpt"
	BaseURL string // https://api.openai.com/v1
	// Token returns a valid access token; Refresh forces a refresh after a 401.
	Token   func(ctx context.Context) (string, error)
	Refresh func(ctx context.Context) error

	mu       sync.Mutex
	rejected map[string]bool // optional request fields this account rejected ("reasoning", ...)
}

var httpClient = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	TLSHandshakeTimeout:   15 * time.Second,
	ResponseHeaderTimeout: 3 * time.Minute,
	IdleConnTimeout:       90 * time.Second,
	ForceAttemptHTTP2:     true,
}}

// optional are request fields that are dropped (and remembered) when the service rejects them.
var optional = []string{"reasoning", "include", "prompt_cache_key", "parallel_tool_calls"}

// Stream sends one request and streams the answer.
func (c *Client) Stream(ctx context.Context, req llm.Request, h llm.Handlers) (*llm.Turn, error) {
	body := map[string]any{
		"model":        req.Model,
		"instructions": req.System,
		"input":        Input(req.History),
		"store":        false,
		"stream":       true,
		"include":      []string{"reasoning.encrypted_content"},
	}
	if len(req.Tools) > 0 {
		body["tools"] = tools(req.Tools)
		body["tool_choice"] = "auto"
	}
	if req.Effort != "" && req.Effort != "auto" {
		body["reasoning"] = map[string]any{"effort": req.Effort, "summary": "auto"}
	} else {
		body["reasoning"] = map[string]any{"summary": "auto"}
	}
	if req.CacheKey != "" {
		body["prompt_cache_key"] = req.CacheKey
	}
	c.mu.Lock()
	for f := range c.rejected {
		delete(body, f)
	}
	c.mu.Unlock()

	refreshed := false
	for attempt := 0; attempt < 4; attempt++ {
		turn, err := c.do(ctx, body, h)
		var ae *apiError
		if !errors.As(err, &ae) {
			return turn, err
		}
		switch {
		case ae.status == 401 && !refreshed && c.Refresh != nil:
			refreshed = true
			if rerr := c.Refresh(ctx); rerr != nil {
				return nil, ae.httpError()
			}
		case ae.code == "subscription_sharing_unsupported_capability" && c.drop(body, ae.param):
			// retry without the field this account doesn't accept
		default:
			if ae.code == "subscription_sharing_usage_limit_exceeded" && h.OnPlanLimits != nil {
				h.OnPlanLimits(llm.PlanLimits{Provider: "chatgpt", At: time.Now(), Source: "ChatGPT reported the limit",
					Windows: []llm.PlanWindow{{Name: "plan_limit", Used: -1, Status: "rejected"}}})
			}
			return nil, ae.httpError()
		}
	}
	return nil, errors.New("the ChatGPT plan request kept failing")
}

// drop removes the rejected optional field named by param (e.g. "reasoning.effort").
func (c *Client) drop(body map[string]any, param string) bool {
	for _, f := range optional {
		if _, ok := body[f]; ok && (param == f || strings.HasPrefix(param, f+".") || strings.HasPrefix(param, f+"[")) {
			delete(body, f)
			c.mu.Lock()
			if c.rejected == nil {
				c.rejected = map[string]bool{}
			}
			c.rejected[f] = true
			c.mu.Unlock()
			return true
		}
	}
	return false
}

// apiError is an error reply from the Responses API (before or during the stream).
type apiError struct {
	status         int
	code, param    string
	message, input string
}

func (e *apiError) Error() string { return e.httpError().Error() }

// httpError turns the API error into what werkz shows, following OpenAI's recovery guidance.
func (e *apiError) httpError() *llm.HTTPError {
	he := &llm.HTTPError{Status: e.status, Body: e.input}
	switch e.code {
	case "subscription_sharing_usage_limit_exceeded":
		he.Reason, he.Fatal = "ChatGPT plan usage limit reached; see "+ManageUsage, true
	case "subscription_sharing_user_not_eligible":
		he.Reason, he.Fatal = "this ChatGPT account can't use its plan in apps", true
	case "subscription_sharing_usage_unavailable", "subscription_sharing_user_unavailable":
		he.Reason = "ChatGPT plan usage is temporarily unavailable; try again shortly"
	case "subscription_sharing_invalid_user", "chatpass_v2_scope_not_authorized", "chatpass_v2_invalid_authorization_context":
		he.Reason, he.Fatal = "the ChatGPT sign-in was rejected; sign in again with /login chatgpt", true
	case "subscription_sharing_unsupported_capability":
		he.Reason = "the ChatGPT plan doesn't support part of this request (" + e.param + ")"
	case "subscription_sharing_route_not_supported":
		he.Reason, he.Fatal = "this ChatGPT plan request type isn't supported", true
	}
	if he.Reason != "" {
		return he
	}
	switch e.status {
	case 401:
		he.Reason, he.Fatal = "the ChatGPT sign-in was rejected; sign in again with /login chatgpt", true
	case 403:
		he.Reason, he.Fatal = "ChatGPT plan access was refused ("+firstNonEmpty(e.message, "policy check")+")", true
	case 429:
		he.Reason = "ChatGPT plan rate limit; see " + ManageUsage
	case 503:
		he.Reason = "ChatGPT plan usage is temporarily unavailable; try again shortly"
	}
	if he.Reason == "" && e.message != "" {
		he.Reason = "ChatGPT plan: " + e.message
	}
	return he
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func parseError(status int, b []byte) *apiError {
	ae := &apiError{status: status, input: string(b)}
	var v struct {
		Error *struct {
			Code    string `json:"code"`
			Param   string `json:"param"`
			Message string `json:"message"`
		} `json:"error"`
		Detail string `json:"detail"`
	}
	if json.Unmarshal(b, &v) == nil {
		if v.Error != nil {
			ae.code, ae.param, ae.message = v.Error.Code, v.Error.Param, v.Error.Message
		} else {
			ae.message = v.Detail
		}
	}
	return ae
}

func (c *Client) do(ctx context.Context, body map[string]any, h llm.Handlers) (*llm.Turn, error) {
	tok, err := c.Token(ctx)
	if err != nil {
		return nil, &llm.HTTPError{Status: 401, Reason: err.Error(), Fatal: true}
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.BaseURL, "/")+"/responses", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "text/event-stream")
	r.Header.Set("Authorization", "Bearer "+tok)
	resp, err := httpClient.Do(r)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if l, ok := headerLimits(resp.Header); ok && h.OnPlanLimits != nil {
		h.OnPlanLimits(l)
	}
	if resp.StatusCode >= 300 {
		eb, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, parseError(resp.StatusCode, eb)
	}
	return readStream(resp.Body, h)
}

// headerLimits reads plan usage from the response headers Codex uses for its limit display, when
// the service sends them. OpenAI doesn't document these for Sign in with ChatGPT, so their absence
// is normal: werkz then points to chatgpt.com/settings/usage instead.
func headerLimits(hd http.Header) (llm.PlanLimits, bool) {
	l := llm.PlanLimits{Provider: "chatgpt", At: time.Now(), Source: "from ChatGPT's response headers (not officially documented)"}
	for _, slot := range []string{"primary", "secondary"} {
		pct := hd.Get("x-codex-" + slot + "-used-percent")
		if pct == "" {
			continue
		}
		used, err := strconv.ParseFloat(pct, 64)
		if err != nil {
			continue
		}
		w := llm.PlanWindow{Name: slot, Used: used / 100}
		switch mins, _ := strconv.Atoi(hd.Get("x-codex-" + slot + "-window-minutes")); {
		case mins == 300:
			w.Name = "five_hour"
		case mins >= 7*24*60-60 && mins <= 7*24*60+60:
			w.Name = "seven_day"
		}
		if secs, err := strconv.Atoi(hd.Get("x-codex-" + slot + "-reset-after-seconds")); err == nil && secs > 0 {
			w.ResetsAt = time.Now().Add(time.Duration(secs) * time.Second)
		}
		if w.Used >= 1 {
			w.Status = "rejected"
		}
		l.Windows = append(l.Windows, w)
	}
	return l, len(l.Windows) > 0
}

// event is one server-sent event of a streamed response (only the fields werkz reads).
type event struct {
	Type     string          `json:"type"`
	Delta    string          `json:"delta"`
	Item     json.RawMessage `json:"item"`
	Code     string          `json:"code"`
	Message  string          `json:"message"`
	Param    string          `json:"param"`
	Response *struct {
		Status string `json:"status"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Incomplete *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Usage *struct {
			Input  int64 `json:"input_tokens"`
			Output int64 `json:"output_tokens"`
			InDet  struct {
				Cached int64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	} `json:"response"`
}

type outputItem struct {
	Type      string          `json:"type"`
	Name      string          `json:"name"`
	CallID    string          `json:"call_id"`
	Arguments string          `json:"arguments"`
	Encrypted string          `json:"encrypted_content"`
	Summary   json.RawMessage `json:"summary"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func readStream(body io.Reader, h llm.Handlers) (*llm.Turn, error) {
	turn := &llm.Turn{}
	var replay []any
	finished := false
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 1024*1024), 32*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var ev event
		if json.Unmarshal([]byte(data), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "response.output_text.delta":
			turn.Text += ev.Delta
			if h.OnText != nil {
				h.OnText(ev.Delta)
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			if h.OnThinking != nil {
				h.OnThinking(ev.Delta)
			}
		case "response.reasoning_summary_part.done":
			if h.OnThinking != nil {
				h.OnThinking("\n\n")
			}
		case "response.output_item.added":
			var it outputItem
			if json.Unmarshal(ev.Item, &it) == nil && it.Type == "function_call" && h.OnToolStart != nil {
				h.OnToolStart(it.Name)
			}
		case "response.output_item.done":
			var it outputItem
			if json.Unmarshal(ev.Item, &it) != nil {
				continue
			}
			switch it.Type {
			case "function_call":
				turn.ToolCalls = append(turn.ToolCalls, llm.ToolCall{ID: it.CallID, Name: it.Name, Args: it.Arguments})
				replay = append(replay, map[string]any{"type": "function_call", "call_id": it.CallID,
					"name": it.Name, "arguments": it.Arguments})
			case "reasoning":
				if it.Encrypted != "" { // only encrypted reasoning can be sent back (store: false)
					summary := it.Summary
					if len(summary) == 0 || string(summary) == "null" {
						summary = json.RawMessage("[]")
					}
					replay = append(replay, map[string]any{"type": "reasoning", "summary": summary,
						"encrypted_content": it.Encrypted})
				}
			case "message":
				var text strings.Builder
				for _, p := range it.Content {
					if p.Type == "output_text" {
						text.WriteString(p.Text)
					}
				}
				if text.Len() > 0 {
					replay = append(replay, map[string]any{"type": "message", "role": "assistant",
						"content": []map[string]any{{"type": "output_text", "text": text.String()}}})
				}
			}
		case "response.completed", "response.incomplete":
			finished = true
			if r := ev.Response; r != nil {
				if u := r.Usage; u != nil {
					turn.Usage = llm.Usage{In: u.Input - u.InDet.Cached, CacheRead: u.InDet.Cached, Out: u.Output}
				}
				turn.StopReason = r.Status
				if ev.Type == "response.incomplete" {
					reason := "unknown reason"
					if r.Incomplete != nil && r.Incomplete.Reason != "" {
						reason = r.Incomplete.Reason
					}
					turn.Note = "the answer was cut short (" + reason + ")"
				}
			}
		case "response.failed":
			ae := &apiError{status: 502, input: data}
			if r := ev.Response; r != nil && r.Error != nil {
				ae.code, ae.message = r.Error.Code, r.Error.Message
				ae.status = statusFor(ae.code)
			}
			return nil, ae
		case "error":
			return nil, &apiError{status: statusFor(ev.Code), code: ev.Code, param: ev.Param, message: ev.Message, input: data}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if !finished {
		return nil, &llm.HTTPError{Status: 502, Reason: "the ChatGPT plan response stream ended early (connection dropped?)"}
	}
	if len(replay) > 0 {
		turn.Items, _ = json.Marshal(replay)
	}
	return turn, nil
}

// statusFor maps documented error codes to their HTTP status, for errors that arrive mid-stream.
func statusFor(code string) int {
	switch code {
	case "subscription_sharing_usage_limit_exceeded":
		return 429
	case "subscription_sharing_user_not_eligible", "subscription_sharing_route_not_supported":
		return 403
	case "subscription_sharing_invalid_user":
		return 401
	case "subscription_sharing_unsupported_capability":
		return 400
	case "subscription_sharing_usage_unavailable", "subscription_sharing_user_unavailable":
		return 503
	}
	return 502
}

// Input converts werkz's provider-neutral history into Responses API input items.
func Input(hist []llm.Msg) []any {
	out := []any{}
	for _, m := range hist {
		switch m.Role {
		case llm.RoleUser:
			out = append(out, map[string]any{"role": "user", "content": m.Text})
		case llm.RoleSystem:
			// "system" items are rejected mid-conversation; developer messages carry the same weight
			out = append(out, map[string]any{"role": "developer", "content": m.Text})
		case llm.RoleAssistant:
			var items []json.RawMessage
			if len(m.RespItems) > 0 && json.Unmarshal(m.RespItems, &items) == nil {
				for _, it := range items {
					out = append(out, it)
				}
				continue
			}
			if m.Text != "" {
				out = append(out, map[string]any{"role": "assistant", "content": m.Text})
			}
			for _, tc := range m.ToolCalls {
				out = append(out, map[string]any{"type": "function_call", "call_id": tc.ID, "name": tc.Name, "arguments": tc.Args})
			}
		case llm.RoleTool:
			out = append(out, map[string]any{"type": "function_call_output", "call_id": m.ToolCallID, "output": m.Text})
		}
	}
	return out
}

func tools(defs []llm.ToolDef) []map[string]any {
	out := make([]map[string]any, 0, len(defs))
	for _, d := range defs {
		req := d.Required
		if req == nil {
			req = []string{}
		}
		out = append(out, map[string]any{"type": "function", "name": d.Name, "description": d.Description,
			"parameters": schemaFor(d, req),
			"strict":     false})
	}
	return out
}

// ListModels returns the models offered to the signed-in account, as the picker shows them.
func (c *Client) ListModels(ctx context.Context) ([]catalog.ModelInfo, error) {
	tok, err := c.Token(ctx)
	if err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(c.BaseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", "Bearer "+tok)
	resp, err := httpClient.Do(r)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		eb, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, parseError(resp.StatusCode, eb).httpError()
	}
	var lr struct {
		Data []struct {
			ID          string `json:"id"`
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
			Visibility  string `json:"visibility"`
			Context     int    `json:"context_window"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return nil, err
	}
	var out []catalog.ModelInfo
	for _, d := range lr.Data {
		if d.Visibility != "" && d.Visibility != "list" {
			continue
		}
		id := firstNonEmpty(d.Slug, d.ID)
		if id == "" {
			continue
		}
		mi := catalog.ModelInfo{ID: id, Label: firstNonEmpty(d.DisplayName, id), Provider: c.ID, Context: 272_000,
			Efforts: catalog.OpenAIEfforts, DefaultEffort: "auto", Note: "your ChatGPT plan"}
		if d.Context > 0 {
			mi.Context = d.Context
		}
		out = append(out, mi)
	}
	return out, nil
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
