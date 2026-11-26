package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// The provider searches below run the search on the user's own model account, so the answer and
// token usage come back with the sources.

// ClaudeSearch uses Anthropic's server-side web search tool.
type ClaudeSearch struct {
	Key, BaseURL string
	Model        string // the user's model; never chosen here
}

// dynamicFilteringModels support the newer web search tool (dynamic filtering).
var dynamicFilteringModels = []string{
	"claude-fable-", "claude-mythos-", "claude-opus-5", "claude-opus-4-8", "claude-opus-4-7",
	"claude-opus-4-6", "claude-sonnet-5", "claude-sonnet-4-6",
}

func hasDynamicFiltering(model string) bool {
	for _, p := range dynamicFilteringModels {
		if strings.HasPrefix(model, p) {
			return true
		}
	}
	return false
}

func (s *ClaudeSearch) Search(ctx context.Context, query string, max int) (*Search, error) {
	query, err := checkQuery(query)
	if err != nil {
		return nil, err
	}
	max = clampMax(max)
	if s.Model == "" {
		return nil, errors.New("Claude web search: no model set")
	}
	opts := []option.RequestOption{option.WithAPIKey(s.Key), option.WithMaxRetries(1)}
	if s.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(s.BaseURL))
	}
	client := anthropic.NewClient(opts...)

	tool := anthropic.ToolUnionParam{OfWebSearchTool20250305: &anthropic.WebSearchTool20250305Param{MaxUses: anthropic.Int(3)}}
	if hasDynamicFiltering(s.Model) {
		tool = anthropic.ToolUnionParam{OfWebSearchTool20260209: &anthropic.WebSearchTool20260209Param{MaxUses: anthropic.Int(3)}}
	}
	msgs := []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(searchPrompt(query)))}

	out := &Search{Query: query, Via: "Claude web search"}
	var c results
	var answer strings.Builder
	// pause_turn: the server paused a long turn; send it back once to let it finish.
	for call := 0; call < 2; call++ {
		callCtx, cancel := context.WithTimeout(ctx, searchTimeout)
		resp, err := client.Messages.New(callCtx, anthropic.MessageNewParams{
			Model:     anthropic.Model(s.Model),
			MaxTokens: 4096,
			Messages:  msgs,
			Tools:     []anthropic.ToolUnionParam{tool},
		})
		cancel()
		if err != nil {
			return nil, fmt.Errorf("Claude web search: %s", redact(err.Error(), s.Key))
		}
		out.InputTokens += resp.Usage.InputTokens
		out.OutputTokens += resp.Usage.OutputTokens
		blocks := 0
		for _, b := range resp.Content {
			switch b.Type {
			case "text":
				answer.WriteString(b.Text)
			case "server_tool_use":
				blocks++
			case "web_search_tool_result":
				if code := b.Content.ErrorCode; code != "" {
					return nil, fmt.Errorf("Claude web search failed: %s", code)
				}
				for _, r := range b.Content.OfWebSearchResultBlockArray {
					c.add(Result{Title: r.Title, URL: r.URL, Age: r.PageAge})
				}
			}
		}
		if n := resp.Usage.ServerToolUse.WebSearchRequests; n > 0 {
			out.Searches += int(n)
		} else {
			out.Searches += blocks
		}
		if resp.StopReason != anthropic.StopReasonPauseTurn {
			break
		}
		msgs = append(msgs, resp.ToParam())
	}
	out.Answer = strings.TrimSpace(answer.String())
	out.Results = c.top(max)
	return out, nil
}

// OpenAISearch uses the Responses API web_search tool.
type OpenAISearch struct {
	Key, BaseURL, Model string // BaseURL "" = https://api.openai.com/v1
	HTTP                *http.Client
}

func (s *OpenAISearch) Search(ctx context.Context, query string, max int) (*Search, error) {
	query, err := checkQuery(query)
	if err != nil {
		return nil, err
	}
	max = clampMax(max)
	if s.Model == "" {
		return nil, errors.New("OpenAI web search: no model set")
	}
	base := strings.TrimRight(s.BaseURL, "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	body, _ := json.Marshal(map[string]any{
		"model":   s.Model,
		"input":   searchPrompt(query),
		"tools":   []any{map[string]any{"type": "web_search"}},
		"include": []string{"web_search_call.action.sources"},
		"store":   false,
	})
	req, err := http.NewRequest(http.MethodPost, base+"/responses", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("OpenAI web search: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.Key)
	var r struct {
		Output []struct {
			Type    string
			Content []struct {
				Type        string
				Text        string
				Annotations []struct{ Type, URL, Title string }
			}
			Action struct {
				Sources []struct{ URL, Title string }
			}
		}
		Usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		}
	}
	if err := doJSON(ctx, s.HTTP, req, "OpenAI web search", s.Key, "", &r); err != nil {
		return nil, err
	}
	out := &Search{Query: query, Via: "OpenAI web search", InputTokens: r.Usage.InputTokens, OutputTokens: r.Usage.OutputTokens}
	var c results
	var answer strings.Builder
	var sources []Result // searched-but-not-cited pages go after the cited ones
	for _, o := range r.Output {
		switch o.Type {
		case "message":
			for _, p := range o.Content {
				if p.Type != "output_text" {
					continue
				}
				answer.WriteString(p.Text)
				for _, a := range p.Annotations {
					if a.Type == "url_citation" {
						c.add(Result{Title: a.Title, URL: a.URL})
					}
				}
			}
		case "web_search_call":
			out.Searches++
			for _, src := range o.Action.Sources {
				sources = append(sources, Result{Title: src.Title, URL: src.URL})
			}
		}
	}
	for _, src := range sources {
		c.add(src)
	}
	out.Answer = strings.TrimSpace(answer.String())
	out.Results = c.top(max)
	return out, nil
}

// GeminiSearch uses generateContent with Google Search grounding.
type GeminiSearch struct {
	Key, BaseURL, Model string // BaseURL "" = https://generativelanguage.googleapis.com/v1beta
	HTTP                *http.Client
}

func (s *GeminiSearch) Search(ctx context.Context, query string, max int) (*Search, error) {
	query, err := checkQuery(query)
	if err != nil {
		return nil, err
	}
	max = clampMax(max)
	if s.Model == "" {
		return nil, errors.New("Gemini web search: no model set")
	}
	base := strings.TrimRight(s.BaseURL, "/")
	if base == "" {
		base = "https://generativelanguage.googleapis.com/v1beta"
	}
	body, _ := json.Marshal(map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": searchPrompt(query)}}}},
		"tools":    []any{map[string]any{"google_search": map[string]any{}}},
	})
	req, err := http.NewRequest(http.MethodPost, base+"/models/"+url.PathEscape(s.Model)+":generateContent", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("Gemini web search: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", s.Key)
	var r struct {
		Candidates []struct {
			Content struct {
				Parts []struct{ Text string }
			}
			GroundingMetadata struct {
				GroundingChunks []struct {
					Web struct{ URI, Title string }
				}
				WebSearchQueries []string
			}
		}
		UsageMetadata struct {
			PromptTokenCount     int64
			CandidatesTokenCount int64
		}
	}
	if err := doJSON(ctx, s.HTTP, req, "Gemini web search", s.Key, "", &r); err != nil {
		return nil, err
	}
	out := &Search{Query: query, Via: "Gemini (Google Search)",
		InputTokens: r.UsageMetadata.PromptTokenCount, OutputTokens: r.UsageMetadata.CandidatesTokenCount}
	if len(r.Candidates) == 0 {
		return nil, errors.New("Gemini web search: no answer (the request may have been blocked)")
	}
	cand := r.Candidates[0]
	var answer strings.Builder
	for _, p := range cand.Content.Parts {
		answer.WriteString(p.Text)
	}
	// The URIs are Google redirect links; they are kept as given.
	var c results
	for _, g := range cand.GroundingMetadata.GroundingChunks {
		c.add(Result{Title: g.Web.Title, URL: g.Web.URI})
	}
	out.Searches = len(cand.GroundingMetadata.WebSearchQueries)
	out.Answer = strings.TrimSpace(answer.String())
	out.Results = c.top(max)
	return out, nil
}
