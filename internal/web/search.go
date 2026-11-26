package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Searcher runs one web search. max is the number of results wanted (default 8, cap 20).
type Searcher interface {
	Search(ctx context.Context, query string, max int) (*Search, error)
}

const (
	defaultMaxResults = 8
	maxMaxResults     = 20
	snippetRunes      = 300
	searchTimeout     = 30 * time.Second
	maxBody           = 4 << 20
)

// searchPrompt is what the provider searches are asked; the answer is only a pointer to sources.
func searchPrompt(query string) string {
	return "Search the web for: " + query + "\nReply with a short answer (under 150 words) that cites the sources you used."
}

func clampMax(n int) int {
	switch {
	case n <= 0:
		return defaultMaxResults
	case n > maxMaxResults:
		return maxMaxResults
	}
	return n
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

// stripHTML removes tags (Brave wraps matches in <strong>) and decodes entities.
func stripHTML(s string) string { return html.UnescapeString(tagRe.ReplaceAllString(s, "")) }

// cut shortens s to n runes (ending in "…" when it was cut) without splitting a character.
func cut(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// results collects Result entries: trimmed, without URL-less or duplicate entries.
type results struct {
	seen map[string]bool
	list []Result
}

func (c *results) add(r Result) {
	r.URL = strings.TrimSpace(r.URL)
	if r.URL == "" || c.seen[r.URL] {
		return
	}
	if c.seen == nil {
		c.seen = map[string]bool{}
	}
	c.seen[r.URL] = true
	r.Title = strings.TrimSpace(r.Title)
	r.Snippet = cut(strings.TrimSpace(r.Snippet), snippetRunes)
	r.Age = strings.TrimSpace(r.Age)
	c.list = append(c.list, r)
}

func (c *results) top(max int) []Result {
	if len(c.list) > max {
		return c.list[:max]
	}
	return c.list
}

// redact removes secrets from text that may echo a request (error bodies, transport errors).
func redact(s string, secrets ...string) string {
	for _, k := range secrets {
		if k != "" {
			s = strings.ReplaceAll(s, k, "[redacted]")
		}
	}
	return s
}

func httpClient(c *http.Client) *http.Client {
	if c == nil {
		return http.DefaultClient
	}
	return c
}

// doJSON sends req (30s limit) and decodes a 2xx JSON body into out. Failures name the service
// and never contain key. hint is appended to a 403/429 failure.
func doJSON(ctx context.Context, c *http.Client, req *http.Request, name, key, hint string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	resp, err := httpClient(c).Do(req.WithContext(ctx))
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%s: no answer within %s", name, searchTimeout)
		}
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // drops the URL, which may carry the instance address
		}
		return fmt.Errorf("%s: %s", name, redact(err.Error(), key))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("%s: reading the response: %s", name, redact(err.Error(), key))
	}
	if resp.StatusCode/100 != 2 {
		msg := strings.Join(strings.Fields(redact(string(body), key)), " ")
		msg = cut(msg, 300)
		e := fmt.Errorf("%s: HTTP %d %s", name, resp.StatusCode, msg)
		if hint != "" && (resp.StatusCode == 403 || resp.StatusCode == 429) {
			e = fmt.Errorf("%w. %s", e, hint)
		}
		return e
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%s: unexpected response (not JSON)", name)
	}
	return nil
}

func checkQuery(q string) (string, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return "", errors.New("empty search query")
	}
	return q, nil
}

// Brave is the Brave Search API.
type Brave struct {
	Key, BaseURL string // BaseURL "" = https://api.search.brave.com/res/v1/web/search
	HTTP         *http.Client
}

func (b *Brave) Search(ctx context.Context, query string, max int) (*Search, error) {
	query, err := checkQuery(query)
	if err != nil {
		return nil, err
	}
	max = clampMax(max)
	base := b.BaseURL
	if base == "" {
		base = "https://api.search.brave.com/res/v1/web/search"
	}
	q := url.Values{"q": {query}, "count": {fmt.Sprint(max)}}
	req, err := http.NewRequest(http.MethodGet, base+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("Brave: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", b.Key)
	var r struct {
		Web struct {
			Results []struct{ Title, URL, Description, Age string }
		}
	}
	if err := doJSON(ctx, b.HTTP, req, "Brave", b.Key, "", &r); err != nil {
		return nil, err
	}
	var c results
	for _, x := range r.Web.Results {
		c.add(Result{Title: stripHTML(x.Title), URL: x.URL, Snippet: stripHTML(x.Description), Age: x.Age})
	}
	return &Search{Query: query, Via: "Brave", Results: c.top(max)}, nil
}

// Tavily is the Tavily search API.
type Tavily struct {
	Key, BaseURL string // BaseURL "" = https://api.tavily.com/search
	HTTP         *http.Client
}

func (t *Tavily) Search(ctx context.Context, query string, max int) (*Search, error) {
	query, err := checkQuery(query)
	if err != nil {
		return nil, err
	}
	max = clampMax(max)
	base := t.BaseURL
	if base == "" {
		base = "https://api.tavily.com/search"
	}
	body, _ := json.Marshal(map[string]any{"query": query, "max_results": max, "search_depth": "basic"})
	req, err := http.NewRequest(http.MethodPost, base, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("Tavily: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.Key)
	var r struct {
		Answer  string
		Results []struct{ Title, URL, Content string }
	}
	if err := doJSON(ctx, t.HTTP, req, "Tavily", t.Key, "", &r); err != nil {
		return nil, err
	}
	var c results
	for _, x := range r.Results {
		c.add(Result{Title: x.Title, URL: x.URL, Snippet: x.Content})
	}
	return &Search{Query: query, Via: "Tavily", Answer: strings.TrimSpace(r.Answer), Results: c.top(max)}, nil
}

// SearXNG is a self-hosted (or public) SearXNG instance; it needs no key.
type SearXNG struct {
	URL  string // instance base URL
	HTTP *http.Client
}

func (s *SearXNG) Search(ctx context.Context, query string, max int) (*Search, error) {
	query, err := checkQuery(query)
	if err != nil {
		return nil, err
	}
	max = clampMax(max)
	if strings.TrimSpace(s.URL) == "" {
		return nil, errors.New("SearXNG: no instance URL set")
	}
	q := url.Values{"q": {query}, "format": {"json"}}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(s.URL, "/")+"/search?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("SearXNG: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	var r struct {
		Results []struct {
			Title, URL, Content string
			PublishedDate       string `json:"publishedDate"`
		}
	}
	const hint = "The instance has to allow JSON output: enable it in its settings.yml (search.formats: [html, json])"
	if err := doJSON(ctx, s.HTTP, req, "SearXNG", "", hint, &r); err != nil {
		return nil, err
	}
	var c results
	for _, x := range r.Results {
		c.add(Result{Title: x.Title, URL: x.URL, Snippet: x.Content, Age: x.PublishedDate})
	}
	return &Search{Query: query, Via: "SearXNG", Results: c.top(max)}, nil
}
