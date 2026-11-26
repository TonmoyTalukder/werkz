package agent

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/catalog"
	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/llm"
	"github.com/TonmoyTalukder/werkz/internal/tools"
	"github.com/TonmoyTalukder/werkz/internal/web"
)

// WebFetch and WebSearch: reading public pages and searching need no approval (they only
// read); a page on the local network (localhost, private addresses, cloud metadata) asks
// first, since it can reach things the internet can't.

const (
	pageChunk    = 20_000 // characters of a page per WebFetch call
	pageCacheTTL = 10 * time.Minute
	pageCacheMax = 8
)

// claudeSearchFee is Anthropic's charge per web search ($10 per 1,000), on top of tokens.
const claudeSearchFee = 0.01

type cachedPage struct {
	page *web.Page
	at   time.Time
}

type pageCache struct {
	mu    sync.Mutex
	pages map[string]cachedPage
}

func (c *pageCache) get(u string) *web.Page {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p, ok := c.pages[u]; ok && time.Since(p.at) < pageCacheTTL {
		return p.page
	}
	return nil
}

func (c *pageCache) put(u string, p *web.Page) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pages == nil {
		c.pages = map[string]cachedPage{}
	}
	if len(c.pages) >= pageCacheMax {
		oldest := ""
		for k, v := range c.pages {
			if oldest == "" || v.at.Before(c.pages[oldest].at) {
				oldest = k
			}
		}
		delete(c.pages, oldest)
	}
	c.pages[u] = cachedPage{p, time.Now()}
}

// localFetch reports whether a WebFetch call reaches the local network, and the host.
func localFetch(ctx context.Context, tc llm.ToolCall) (local bool, host string) {
	if tc.Name != "WebFetch" {
		return false, ""
	}
	a, err := tools.ParseArgs(tc)
	if err != nil {
		return false, ""
	}
	u, err := url.Parse(a.URL)
	if err != nil {
		return false, ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	local, _ = web.IsLocal(ctx, a.URL, nil)
	return local, u.Hostname()
}

func (e *Engine) webFetch(ctx context.Context, tc llm.ToolCall, allowLocal bool) tools.Result {
	a, err := tools.ParseArgs(tc)
	if err != nil || strings.TrimSpace(a.URL) == "" {
		return tools.Fail("WebFetch needs a url")
	}
	page := e.pages.get(a.URL)
	if page == nil || a.Offset == 0 {
		ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		f := &web.Fetcher{AllowLocal: allowLocal}
		page, err = f.Fetch(ctx, a.URL)
		if err != nil {
			return tools.Fail("Couldn't fetch %s: %v", a.URL, err)
		}
		e.pages.put(a.URL, page)
	}
	chunk, next := web.Paginate(page.Markdown, max(a.Offset, 0), pageChunk)
	var b strings.Builder
	b.WriteString(chunk)
	total := len([]rune(page.Markdown))
	if next > 0 {
		fmt.Fprintf(&b, "\n\n… %d more characters: call WebFetch with offset=%d to continue.", total-next, next)
	}
	if page.Truncated {
		b.WriteString("\n\n(The page was larger than werkz reads; the end is missing.)")
	}
	sum := fmt.Sprintf("%s · %s", humanSize(page.Bytes), page.ContentType)
	if page.Title != "" {
		sum = page.Title + " · " + humanSize(page.Bytes)
	}
	if a.Offset > 0 {
		sum += fmt.Sprintf(" · from character %d", a.Offset)
	}
	return tools.Result{Output: web.Untrusted("page", page.URL, b.String()), Summary: sum}
}

func humanSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}

// errNoSearch explains how to set up search when nothing can search.
var errNoSearch = errors.New("no web search is set up: the current model's provider has none. " +
	"Set one with /search (Brave or Tavily key, or a SearXNG instance), or switch to a Claude, OpenAI or Gemini model")

// searcher picks the search backend: the user's choice, or (auto) the current provider's own
// search, then a search service the user set up.
func (e *Engine) searcher() (web.Searcher, string, error) { return searcherFor(e.cfg) }

// SearchStatus says which search WebSearch would use now, for /search.
func SearchStatus(cfg *config.Config) (string, error) {
	s, provider, err := searcherFor(cfg)
	if err != nil {
		return "", err
	}
	switch v := s.(type) {
	case *web.ClaudeSearch:
		return "Claude web search, on " + v.Model + " (tokens plus $10 per 1,000 searches)", nil
	case *web.OpenAISearch:
		return "OpenAI web search, on " + v.Model + " (tokens plus OpenAI's search fee)", nil
	case *web.GeminiSearch:
		return "Gemini with Google Search, on " + v.Model + " (Google's free quota, then its fee)", nil
	case *web.Brave:
		return "Brave Search API", nil
	case *web.Tavily:
		return "Tavily", nil
	case *web.SearXNG:
		return "SearXNG at " + v.URL, nil
	}
	return provider, nil
}

func searcherFor(cfg *config.Config) (web.Searcher, string, error) {
	service := func(id string) web.Searcher {
		switch id {
		case "brave":
			if k := cfg.SearchKey("brave"); k != "" {
				return &web.Brave{Key: k}
			}
		case "tavily":
			if k := cfg.SearchKey("tavily"); k != "" {
				return &web.Tavily{Key: k}
			}
		case "searxng":
			if u := cfg.SearXNG(); u != "" {
				return &web.SearXNG{URL: u}
			}
		}
		return nil
	}
	provider := func() (web.Searcher, string) {
		p, model := cfg.Provider, cfg.Model
		switch p {
		case "anthropic":
			if k := cfg.Key(p); k != "" {
				return &web.ClaudeSearch{Key: k, Model: model}, p
			}
		case "openai":
			if k := cfg.Key(p); k != "" {
				return &web.OpenAISearch{Key: k, BaseURL: cfg.BaseURL(p), Model: model}, p
			}
		case "gemini":
			if k := cfg.Key(p); k != "" {
				return &web.GeminiSearch{Key: k, Model: model}, p
			}
		}
		return nil, ""
	}
	switch cfg.WebSearch {
	case "off":
		return nil, "", errors.New("web search is turned off (/search auto turns it on)")
	case "brave", "tavily", "searxng":
		if s := service(cfg.WebSearch); s != nil {
			return s, "", nil
		}
		return nil, "", fmt.Errorf("web search is set to %s, but it has no key or address (see /search)", cfg.WebSearch)
	case "provider":
		if s, p := provider(); s != nil {
			return s, p, nil
		}
		return nil, "", errNoSearch
	}
	if s, p := provider(); s != nil {
		return s, p, nil
	}
	for _, id := range []string{"brave", "tavily", "searxng"} {
		if s := service(id); s != nil {
			return s, "", nil
		}
	}
	return nil, "", errNoSearch
}

func (e *Engine) webSearch(ctx context.Context, tc llm.ToolCall) tools.Result {
	a, err := tools.ParseArgs(tc)
	if err != nil || strings.TrimSpace(a.Query) == "" {
		return tools.Fail("WebSearch needs a query")
	}
	s, provider, err := e.searcher()
	if err != nil {
		return tools.Fail("%v", err)
	}
	n := a.MaxResults
	if n <= 0 {
		n = 8
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	res, err := s.Search(ctx, a.Query, min(n, 20))
	if err != nil {
		return tools.Fail("Search failed: %v", err)
	}
	if provider != "" {
		e.chargeSearch(provider, res)
	}
	sum := fmt.Sprintf("%d result%s · %s", len(res.Results), map[bool]string{true: "", false: "s"}[len(res.Results) == 1], res.Via)
	return tools.Result{Output: res.Format(), Summary: sum}
}

// chargeSearch reports the cost of a provider search (its tokens, plus Claude's per-search fee).
func (e *Engine) chargeSearch(provider string, res *web.Search) {
	mi := catalog.Lookup(provider, e.cfg.Model)
	u := llm.Usage{In: res.InputTokens, Out: res.OutputTokens}
	cost := mi.Cost(u.In, u.Out, 0, 0, false, false)
	unpriced := !mi.Priced()
	switch provider {
	case "anthropic":
		cost += float64(res.Searches) * claudeSearchFee
	default:
		unpriced = unpriced || res.Searches > 0 // OpenAI and Google charge per search too
	}
	e.send(UsageMsg{Provider: provider, Model: e.cfg.Model, Usage: u, Cost: cost, Sub: true, Unpriced: unpriced})
}

// webToolsNote is how the ask for a local page reads.
func localAskSummary(summary, host string) string {
	return summary + "  ⚠ local network (" + host + ")"
}
