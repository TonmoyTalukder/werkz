package tui

import (
	"net/url"
	"strings"

	"github.com/TonmoyTalukder/werkz/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
)

// searchCommand handles /search: which web search WebSearch uses, and setting one up.
//
//	/search                     what is used now, and the options
//	/search auto|provider|off   choose how (auto: the model's own search, else a service below)
//	/search brave <key>         Brave Search API key (also BRAVE_API_KEY)
//	/search tavily <key>        Tavily key (also TAVILY_API_KEY)
//	/search searxng <url>       a SearXNG instance (also SEARXNG_URL)
func (m *model) searchCommand(args []string) tea.Cmd {
	p := m.printS
	if len(args) == 0 {
		return m.push(func(int) string { return m.searchText() })
	}
	switch what := strings.ToLower(args[0]); what {
	case "auto", "provider", "off":
		m.cfg.WebSearch = what
		if what == "auto" {
			m.cfg.WebSearch = ""
		}
		m.saveConfig()
		return m.push(func(int) string { return m.searchText() })
	case "brave", "tavily":
		if len(args) < 2 {
			if m.cfg.SearchKey(what) == "" {
				return p(renderNotice("warn", "Add a key: /search "+what+" <key>"))
			}
			m.cfg.WebSearch = what
			m.saveConfig()
			return m.push(func(int) string { return m.searchText() })
		}
		if args[1] == "remove" {
			delete(m.cfg.Keys, what)
			if m.cfg.WebSearch == what {
				m.cfg.WebSearch = ""
			}
			m.saveConfig()
			return p(renderNotice("info", "Removed the "+what+" key."))
		}
		m.cfg.Keys[what] = strings.TrimSpace(args[1])
		m.cfg.WebSearch = what
		m.saveConfig()
		return p(renderNotice("info", "Saved the "+what+" key ("+maskKey(args[1])+"); WebSearch now uses "+what+"."))
	case "searxng":
		if len(args) < 2 {
			return p(renderNotice("warn", "Give the instance's address: /search searxng https://search.example.org"))
		}
		if args[1] == "remove" {
			m.cfg.SearXNGURL = ""
			if m.cfg.WebSearch == "searxng" {
				m.cfg.WebSearch = ""
			}
			m.saveConfig()
			return p(renderNotice("info", "Removed the SearXNG address."))
		}
		u, err := url.Parse(args[1])
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return p(renderNotice("warn", "That isn't an http(s) address."))
		}
		m.cfg.SearXNGURL = strings.TrimRight(args[1], "/")
		m.cfg.WebSearch = "searxng"
		m.saveConfig()
		return p(renderNotice("info", "WebSearch now uses SearXNG at "+m.cfg.SearXNGURL+" (its JSON output must be on: search.formats: [html, json])."))
	}
	return p(renderNotice("warn", "Usage: /search · /search auto|provider|off · /search brave|tavily <key> · /search searxng <url>"))
}

func maskKey(k string) string {
	k = strings.TrimSpace(k)
	if len(k) <= 8 {
		return "…"
	}
	return k[:4] + "…" + k[len(k)-4:]
}

func (m *model) searchText() string {
	w := max(m.w-8, 30)
	var lines []string
	add := func(s string) { lines = append(lines, indent(wrapTo(s, w), "  ", "    ")) }
	mode := m.cfg.WebSearch
	if mode == "" {
		mode = "auto"
	}
	if s, err := agent.SearchStatus(m.cfg); err != nil {
		add(sYellow.Render("✗ ") + sText.Render(err.Error()))
	} else {
		add(sGreen.Render("✓ ") + sText.Render("Now: "+s) + sDim.Render("  ("+mode+")"))
	}
	lines = append(lines, "")
	have := func(ok bool, name, how string) {
		mark := sDim.Render("· ")
		if ok {
			mark = sGreen.Render("✓ ")
		}
		add(mark + sText.Render(padRight(name, 18)) + sDim.Render(how))
	}
	have(m.cfg.SearchKey("brave") != "", "Brave Search API", "/search brave <key> · free tier at brave.com/search/api")
	have(m.cfg.SearchKey("tavily") != "", "Tavily", "/search tavily <key> · free tier at tavily.com")
	have(m.cfg.SearXNG() != "", "SearXNG", "/search searxng <url> · self-hosted, no key")
	lines = append(lines, "")
	add(sDim.Render("auto uses the model's own search (Claude, OpenAI, Gemini), else one of these · /search off turns it off"))
	add(sDim.Render("Reading pages (WebFetch) needs no setup; pages on your local network ask first."))
	return box(gradient("Web search", 0)+"\n"+strings.Join(lines, "\n"), cSky, m.w)
}
