// Command sitegen builds the werkz website: it copies site/, renders docs/*.md into docs pages,
// and fills the landing page's documentation list and release notes (from git tags).
//
//	go run ./tools/sitegen -out _site
//
// The result is static files for GitHub Pages; nothing here runs in the browser.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	ghtml "github.com/yuin/goldmark/renderer/html"
)

const repo = "https://github.com/TonmoyTalukder/werkz"

// githubPath is the GitHub mark (Octicons), used for links to the repository.
const githubPath = "M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8z"

// doc is one documentation page, in reading order.
type doc struct {
	Slug, Title, Blurb string
	Src                string // source file, relative to the repository (default docs/<slug>.md)
}

var docs = []doc{
	{"installation", "Installation", "Installer, Homebrew, apt, dnf, package files, updating, uninstalling", ""},
	{"getting-started", "Getting started", "First run and your first task", ""},
	{"usage", "Usage", "Commands, @ mentions, copying, saved conversations, web access, the browser, MCP", ""},
	{"providers-and-models", "Providers and models", "API keys, subscriptions, free and local models, effort", ""},
	{"safety", "Safety and privacy", "Permission modes and the rules that always apply", ""},
	{"fallback", "Fallback", "What happens when a model fails", ""},
	{"workspaces", "Workspaces", "Working across several repositories", ""},
	{"saving-tokens", "Saving tokens and money", "Caching, compaction, context window, cost", ""},
	{"configuration", "Configuration", "Config files and environment variables", ""},
	{"troubleshooting", "Troubleshooting", "Common problems and fixes", ""},
	{"development", "Development", "Building, testing, releasing, project layout", ""},
	{"contributing", "Contributing", "Report a bug, set up, and send a change", "CONTRIBUTING.md"},
}

func main() {
	out := flag.String("out", "_site", "output folder")
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if err := build(*root, *out); err != nil {
		log.Fatal(err)
	}
}

func build(root, out string) error {
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	if err := copyDir(filepath.Join(root, "site"), out); err != nil {
		return err
	}
	rels, err := releases(root)
	if err != nil {
		return err
	}

	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(ghtml.WithUnsafe()),
	)
	pageT := template.Must(template.New("doc").Parse(docTemplate))
	if err := os.MkdirAll(filepath.Join(out, "docs"), 0o755); err != nil {
		return err
	}
	for i, d := range docs {
		path := filepath.Join(root, "docs", d.Slug+".md")
		if d.Src != "" {
			path = filepath.Join(root, d.Src)
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var body bytes.Buffer
		if err := md.Convert(src, &body); err != nil {
			return fmt.Errorf("%s: %w", d.Slug, err)
		}
		content, title, toc := polish(body.String(), d.Title)
		p := docPage{Doc: d, Title: title, Content: template.HTML(content), TOC: toc, Docs: docs, Nav: navLinks(d.Slug)}
		if i > 0 {
			p.Prev = &docs[i-1]
		}
		if i < len(docs)-1 {
			p.Next = &docs[i+1]
		}
		if err := writeTemplate(pageT, filepath.Join(out, "docs", d.Slug+".html"), p); err != nil {
			return err
		}
	}
	// docs/index.html and the release notes page share the same shell.
	idx := docPage{Doc: doc{Slug: "index", Title: "Documentation"}, Title: "Documentation", Docs: docs, Nav: navLinks("index"),
		Content: template.HTML("<p>Everything about installing, using and configuring werkz.</p>\n<ul class=\"doclist\">" + docList("") + "</ul>")}
	if err := writeTemplate(pageT, filepath.Join(out, "docs", "index.html"), idx); err != nil {
		return err
	}
	rn := docPage{Doc: doc{Slug: "releases", Title: "Release notes"}, Title: "Release notes", Docs: docs, Nav: navLinks("releases"),
		Content: template.HTML("<p>What changed in each version of werkz, newest first. Downloads are on the <a href=\"" + repo + "/releases\">releases page</a>.</p>\n" + releaseTree(rels, 0, true))}
	if err := writeTemplate(pageT, filepath.Join(out, "docs", "releases.html"), rn); err != nil {
		return err
	}

	// Landing page: documentation list and the latest release notes.
	landing := filepath.Join(out, "index.html")
	b, err := os.ReadFile(landing)
	if err != nil {
		return err
	}
	s := strings.Replace(string(b), "<!-- DOCS-LIST -->", docList("docs/"), 1)
	s = strings.Replace(s, "<!-- RELEASES -->", releaseTree(rels, 5, false), 1)
	if len(rels) > 0 {
		s = strings.ReplaceAll(s, "<!-- VERSION -->", html.EscapeString(rels[0].Version))
	}
	return os.WriteFile(landing, []byte(s), 0o644)
}

func docList(prefix string) string {
	var b strings.Builder
	for _, d := range docs {
		fmt.Fprintf(&b, "\n      <li><a href=\"%s%s.html\"><span><b>%s</b><span>%s</span></span><svg aria-hidden=\"true\" viewBox=\"0 0 26 12\"><path d=\"M0 6h24M19 1l5 5-5 5\" fill=\"none\" stroke=\"currentColor\" stroke-width=\"1.2\"/></svg></a></li>",
			prefix, d.Slug, html.EscapeString(d.Title), html.EscapeString(d.Blurb))
	}
	return b.String() + "\n    "
}

type navLink struct {
	Href, Label string
	Current     bool
}

func navLinks(cur string) []navLink {
	// the same menu as the home page, in the same order
	return []navLink{
		{"../#features", "Features", false},
		{"./", "Docs", cur != "releases" && cur != "contributing"},
		{"../#install", "Install", false},
		{"../#subscriptions", "Subscriptions", false},
		{"releases.html", "Releases", cur == "releases"},
		{"contributing.html", "Contribute", cur == "contributing"},
	}
}

// ---------------------------------------------------------------- doc rendering

var (
	h1Re      = regexp.MustCompile(`(?s)<h1[^>]*>(.*?)</h1>\n?`)
	headingRe = regexp.MustCompile(`<h([23]) id="([^"]+)">(.*?)</h[23]>`)
	hrefRe    = regexp.MustCompile(`href="([^"]+)"`)
	tagRe     = regexp.MustCompile(`<[^>]+>`)
)

type tocEntry struct{ ID, Text string }

// polish takes the page's own h1 as the title, adds heading anchors, collects the table of
// contents, and points Markdown links at the rendered pages (or GitHub for repository files).
func polish(body, fallback string) (content, title string, toc []tocEntry) {
	title = fallback
	if m := h1Re.FindStringSubmatch(body); m != nil {
		title = html.UnescapeString(tagRe.ReplaceAllString(m[1], ""))
		body = strings.Replace(body, m[0], "", 1)
	}
	body = headingRe.ReplaceAllStringFunc(body, func(h string) string {
		m := headingRe.FindStringSubmatch(h)
		if m[1] == "2" {
			toc = append(toc, tocEntry{ID: m[2], Text: html.UnescapeString(tagRe.ReplaceAllString(m[3], ""))})
		}
		return fmt.Sprintf(`<h%s id="%s">%s<a class="anchor" href="#%s" aria-label="Link to this section">#</a></h%s>`, m[1], m[2], m[3], m[2], m[1])
	})
	body = hrefRe.ReplaceAllStringFunc(body, func(a string) string {
		u := hrefRe.FindStringSubmatch(a)[1]
		return `href="` + rewriteLink(u) + `"`
	})
	return body, title, toc
}

func rewriteLink(u string) string {
	if strings.Contains(u, "://") || strings.HasPrefix(u, "#") || strings.HasPrefix(u, "mailto:") {
		return u
	}
	path, frag, _ := strings.Cut(u, "#")
	if frag != "" {
		frag = "#" + frag
	}
	switch {
	case strings.HasPrefix(path, "docs/") && strings.HasSuffix(path, ".md"):
		return strings.TrimSuffix(strings.TrimPrefix(path, "docs/"), ".md") + ".html" + frag // from CONTRIBUTING.md
	case path == "docs/":
		return "./" + frag
	case path == "LICENSE" || path == "README.md":
		return repo + "/blob/main/" + path + frag
	case strings.HasPrefix(path, "../"):
		return repo + "/blob/main/" + strings.TrimPrefix(path, "../") + frag
	case strings.HasSuffix(path, ".md") && !strings.Contains(path, "/"):
		name := strings.TrimSuffix(path, ".md")
		if name == "README" {
			return "./" + frag
		}
		return name + ".html" + frag
	}
	return repo + "/blob/main/docs/" + path + frag
}

// ---------------------------------------------------------------- release notes

type release struct {
	Version, Date string
	Groups        []group
}

type group struct {
	Label string
	Items []string
}

var subjectRe = regexp.MustCompile(`^(\w+)(?:\(([^)]*)\))?!?:\s*(.+)$`)

// releases reads the version tags, newest first, and the user-facing commits in each.
func releases(root string) ([]release, error) {
	tags, err := git(root, "tag", "--list", "v*", "--sort=-v:refname")
	if err != nil {
		return nil, err
	}
	list := strings.Fields(tags)
	var out []release
	for i, t := range list {
		rng := t
		if i+1 < len(list) {
			rng = list[i+1] + ".." + t
		}
		date, err := git(root, "log", "-1", "--format=%cs", t)
		if err != nil {
			return nil, err
		}
		subjects, err := git(root, "log", "--no-merges", "--format=%s", rng)
		if err != nil {
			return nil, err
		}
		r := release{Version: strings.TrimPrefix(t, "v"), Date: strings.TrimSpace(date)}
		byLabel := map[string]*group{}
		order := []string{"New", "Fixed", "Packaging"}
		for _, s := range strings.Split(strings.TrimSpace(subjects), "\n") {
			m := subjectRe.FindStringSubmatch(strings.TrimSpace(s))
			if m == nil {
				continue
			}
			label := map[string]string{"feat": "New", "fix": "Fixed", "perf": "New", "build": "Packaging"}[m[1]]
			if label == "" {
				continue // chores, docs, tests, refactors and release commits stay out of user-facing notes
			}
			g := byLabel[label]
			if g == nil {
				g = &group{Label: label}
				byLabel[label] = g
			}
			text := m[3]
			g.Items = append(g.Items, strings.ToUpper(text[:1])+text[1:])
		}
		for _, l := range order {
			if g := byLabel[l]; g != nil {
				// git log lists newest first; read each release top-down in the order work landed
				for a, b := 0, len(g.Items)-1; a < b; a, b = a+1, b-1 {
					g.Items[a], g.Items[b] = g.Items[b], g.Items[a]
				}
				r.Groups = append(r.Groups, *g)
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// releaseTree renders releases as a vertical tree, newest first; limit > 0 shows that many
// (the rest link to the full page); open expands every release instead of only the newest.
func releaseTree(rels []release, limit int, open bool) string {
	var b strings.Builder
	b.WriteString(`<ol class="releases">`)
	for i, r := range rels {
		if limit > 0 && i >= limit {
			break
		}
		attr := ""
		if i == 0 || open {
			attr = " open"
		}
		latest := ""
		if i == 0 {
			latest = `<span class="latest caps">Latest</span>`
		}
		fmt.Fprintf(&b, "\n  <li class=\"release\"><details%s><summary><span class=\"ver\">v%s</span>%s<time datetime=\"%s\">%s</time></summary>",
			attr, html.EscapeString(r.Version), latest, r.Date, r.Date)
		if len(r.Groups) == 0 {
			b.WriteString(`<p class="quiet">Maintenance release: packaging and documentation.</p>`)
		}
		for _, g := range r.Groups {
			items := g.Items
			more := 0
			if !open && len(items) > 6 {
				more, items = len(items)-6, items[:6]
			}
			fmt.Fprintf(&b, `<div class="rgroup"><h3>%s</h3><ul>`, g.Label)
			for _, it := range items {
				fmt.Fprintf(&b, "<li>%s</li>", inlineCode(it))
			}
			if more > 0 {
				fmt.Fprintf(&b, `<li class="quiet">and %d more</li>`, more)
			}
			b.WriteString("</ul></div>")
		}
		fmt.Fprintf(&b, `<a class="rlink" href="%s/releases/tag/v%s">Downloads for v%s</a></details></li>`, repo, html.EscapeString(r.Version), html.EscapeString(r.Version))
	}
	b.WriteString("\n</ol>")
	return b.String()
}

// codeRe finds slash commands (/update, /login) and flags (--update) that start a word; paths
// like deb/rpm don't match.
var codeRe = regexp.MustCompile(`(^|[\s(])(/[a-z][a-z-]*|--[a-z][a-z-]*)([\s,.)]|$)`)

// inlineCode escapes a commit subject and sets slash commands in code.
func inlineCode(s string) string {
	s = html.EscapeString(s)
	return codeRe.ReplaceAllString(s, "$1<code>$2</code>$3")
}

func git(root string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	b, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(b), nil
}

// ---------------------------------------------------------------- files

type docPage struct {
	Doc        doc
	Title      string
	Content    template.HTML
	TOC        []tocEntry
	Docs       []doc
	Nav        []navLink
	Prev, Next *doc
}

func writeTemplate(t *template.Template, path string, data any) error {
	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return err
	}
	return os.WriteFile(path, b.Bytes(), 0o644)
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

const docTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · werkz</title>
<meta name="description" content="{{.Doc.Blurb}}">
<meta name="theme-color" content="#ffffff" media="(prefers-color-scheme: light)">
<meta name="theme-color" content="#0c0d14" media="(prefers-color-scheme: dark)">
<link rel="icon" href="../assets/favicon.png">
<link rel="preload" href="../fonts/Geist-300-700.woff2" as="font" type="font/woff2" crossorigin>
<script>(() => { let t = null; try { t = localStorage.getItem("werkz-theme"); } catch {} const q = new URLSearchParams(location.search).get("theme"); if (q === "light" || q === "dark") t = q; if (t !== "light" && t !== "dark") t = "light"; document.documentElement.dataset.theme = t; })();</script>
<link rel="stylesheet" href="../styles.css">
</head>
<body class="docs">
<a class="skip" href="#doc">Skip to content</a>
<svg width="0" height="0" style="position:absolute" aria-hidden="true">
  <symbol id="i-github" viewBox="0 0 16 16"><path fill="currentColor" d="` + githubPath + `"/></symbol>
  <symbol id="i-sun" viewBox="0 0 24 24"><g fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></g></symbol>
  <symbol id="i-moon" viewBox="0 0 24 24"><path fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round" d="M20.5 14.5A8.5 8.5 0 0 1 9.5 3.5a8.5 8.5 0 1 0 11 11z"/></symbol>
  <symbol id="i-menu" viewBox="0 0 24 24"><path fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" d="M4 7h16M4 12h16M4 17h16"/></symbol>
  <symbol id="i-copy" viewBox="0 0 24 24"><g fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"><rect x="8" y="8" width="12" height="12" rx="2.5"/><path d="M16 8V6.5A2.5 2.5 0 0 0 13.5 4h-7A2.5 2.5 0 0 0 4 6.5v7A2.5 2.5 0 0 0 6.5 16H8"/></g></symbol>
  <symbol id="i-check" viewBox="0 0 24 24"><path fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" d="M5 12.5l4.5 4.5L19 7.5"/></symbol>
</svg>
<header class="top">
  <div class="top-inner">
    <a class="brand" href="../" aria-label="werkz home"><img class="logo-tile" src="../assets/werkz.png" alt="" width="28" height="28"><svg class="logo-mark" viewBox="0 0 100 100" width="28" height="28" aria-hidden="true"><defs><linearGradient id="wg-head" gradientUnits="userSpaceOnUse" x1="20" y1="45" x2="80" y2="56"><stop offset="0" stop-color="#ff9933"/><stop offset="0.5" stop-color="#e8344e"/><stop offset="1" stop-color="#3b6cf6"/></linearGradient></defs><polyline points="20,30 33.5,68 50,40 66.5,68 80,30" fill="none" stroke="url(#wg-head)" stroke-width="10.5" stroke-linecap="round" stroke-linejoin="round"/><rect x="43" y="77.5" width="14" height="4" rx="2" fill="#ff9933"/></svg>werkz</a>
    <nav id="topnav" aria-label="Main">{{range .Nav}}
      <a href="{{.Href}}"{{if .Current}} aria-current="page"{{end}}>{{.Label}}</a>{{end}}
    </nav>
    <div class="top-actions">
      <button class="icon-btn theme-toggle" type="button" aria-label="Switch between light and dark theme"><svg class="icon sun" aria-hidden="true"><use href="#i-sun"/></svg><svg class="icon moon" aria-hidden="true"><use href="#i-moon"/></svg></button>
      <a class="icon-btn" href="https://github.com/TonmoyTalukder/werkz" aria-label="werkz on GitHub"><svg class="icon" aria-hidden="true"><use href="#i-github"/></svg><span class="gh-label">GitHub</span></a>
      <button class="icon-btn menu-toggle" type="button" aria-expanded="false" aria-controls="topnav" aria-label="Menu"><svg class="icon" aria-hidden="true"><use href="#i-menu"/></svg></button>
    </div>
  </div>
</header>
<div class="docs-shell">
  <nav class="docs-nav" aria-label="Documentation">
    <details open><summary class="caps">Documentation</summary>
    <ol>{{$cur := .Doc.Slug}}{{range .Docs}}
      <li><a href="{{.Slug}}.html"{{if eq .Slug $cur}} aria-current="page"{{end}}>{{.Title}}</a></li>{{end}}
      <li><a href="releases.html"{{if eq "releases" $cur}} aria-current="page"{{end}}>Release notes</a></li>
    </ol>
    </details>
  </nav>
  <article class="doc" id="doc">
    <h1>{{.Title}}</h1>
    {{.Content}}
    {{if or .Prev .Next}}<nav class="doc-foot" aria-label="Previous and next">
      {{with .Prev}}<a href="{{.Slug}}.html"><small class="caps">Previous</small>{{.Title}}</a>{{else}}<span></span>{{end}}
      {{with .Next}}<a href="{{.Slug}}.html" style="text-align:right"><small class="caps">Next</small>{{.Title}}</a>{{end}}
    </nav>{{end}}
    {{if .Doc.Blurb}}<p class="edit"><a href="https://github.com/TonmoyTalukder/werkz/blob/main/{{if .Doc.Src}}{{.Doc.Src}}{{else}}docs/{{.Doc.Slug}}.md{{end}}">Edit this page on GitHub</a></p>{{end}}
  </article>
  {{if .TOC}}<aside class="toc" aria-label="On this page"><p class="caps">On this page</p><ol>{{range .TOC}}
    <li><a href="#{{.ID}}">{{.Text}}</a></li>{{end}}
  </ol></aside>{{end}}
</div>
<footer class="foot">
  <div class="foot-base" style="border-top:0;padding-top:24px">
    <span>© Tonmoy Talukder and contributors · Apache 2.0</span>
    <a href="https://github.com/TonmoyTalukder/werkz"><svg class="icon" aria-hidden="true"><use href="#i-github"/></svg>TonmoyTalukder/werkz</a>
  </div>
</footer>
<script src="../site.js" defer></script>
</body>
</html>
`
