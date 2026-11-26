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

// doc is one documentation page, in reading order.
type doc struct {
	Slug, Title, Blurb string
}

var docs = []doc{
	{"installation", "Installation", "Installer, Homebrew, apt, dnf, package files, updating, uninstalling"},
	{"getting-started", "Getting started", "First run and your first task"},
	{"usage", "Usage", "Commands, keyboard shortcuts, command-line flags, tools"},
	{"providers-and-models", "Providers and models", "API keys, subscriptions, free and local models, effort"},
	{"safety", "Safety and privacy", "Permission modes and the rules that always apply"},
	{"fallback", "Fallback", "What happens when a model fails"},
	{"workspaces", "Workspaces", "Working across several repositories"},
	{"saving-tokens", "Saving tokens and money", "Caching, compaction, context window, cost"},
	{"configuration", "Configuration", "Config files and environment variables"},
	{"troubleshooting", "Troubleshooting", "Common problems and fixes"},
	{"development", "Development", "Building, testing, releasing, project layout"},
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
		src, err := os.ReadFile(filepath.Join(root, "docs", d.Slug+".md"))
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
	return []navLink{
		{"../#install", "Install", false},
		{"../#models", "Models", false},
		{"../#subscriptions", "Subscriptions", false},
		{"releases.html", "Releases", cur == "releases"},
		{"./", "Docs", cur != "releases"},
		{repo, "GitHub", false},
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
			fmt.Fprintf(&b, `<div class="rgroup"><h4 class="caps">%s</h4><ul>`, g.Label)
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
<meta name="theme-color" content="#2b2f7f">
<link rel="icon" href="../assets/favicon.png">
<link rel="preload" href="../fonts/Jost-200-600.woff2" as="font" type="font/woff2" crossorigin>
<link rel="stylesheet" href="../styles.css">
</head>
<body class="docs">
<a class="skip" href="#doc">Skip to content</a>
<header class="top">
  <div class="top-inner">
    <a class="wordmark" href="../" aria-label="werkz home"><img src="../assets/werkz.png" alt="" width="28" height="28">werkz</a>
    <button class="menu-toggle" type="button" aria-expanded="false" aria-controls="topnav" aria-label="Menu">
      <svg width="20" height="14" viewBox="0 0 20 14" aria-hidden="true"><path d="M0 1h20M0 7h20M0 13h20" stroke="currentColor" stroke-width="1.4"/></svg>
    </button>
    <nav id="topnav" aria-label="Main">{{range .Nav}}
      <a href="{{.Href}}"{{if .Current}} aria-current="page"{{end}}>{{.Label}}</a>{{end}}
    </nav>
  </div>
</header>
<div class="docs-ribbon" aria-hidden="true"><svg viewBox="0 0 1200 14" preserveAspectRatio="none"><defs><filter id="sheer" x="0" y="0" width="100%" height="100%"><feTurbulence type="fractalNoise" baseFrequency="0.85 0.06" numOctaves="2" seed="7" result="weave"/><feColorMatrix in="weave" type="matrix" values="0 0 0 0 1  0 0 0 0 1  0 0 0 0 1  0 0 0 0.16 0" result="threads"/><feComposite in="threads" in2="SourceGraphic" operator="in" result="lit"/><feBlend in="lit" in2="SourceGraphic" mode="screen"/></filter></defs><path d="M0 0H700L620 14H0Z" fill="#2b2f7f" fill-opacity=".9" filter="url(#sheer)"/><path d="M420 0H980L900 14H340Z" fill="#b23a65" fill-opacity=".8" style="mix-blend-mode:multiply" filter="url(#sheer)"/><path d="M760 0H1200V14H680Z" fill="#f2b31a" fill-opacity=".85" style="mix-blend-mode:multiply" filter="url(#sheer)"/></svg></div>
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
    {{if .Doc.Blurb}}<p class="edit"><a href="https://github.com/TonmoyTalukder/werkz/blob/main/docs/{{.Doc.Slug}}.md">Edit this page on GitHub</a></p>{{end}}
  </article>
  {{if .TOC}}<aside class="toc" aria-label="On this page"><p class="caps">On this page</p><ol>{{range .TOC}}
    <li><a href="#{{.ID}}">{{.Text}}</a></li>{{end}}
  </ol></aside>{{end}}
</div>
<footer class="foot">
  <span>werkz · Apache License 2.0 · Tonmoy Talukder and contributors</span>
  <nav aria-label="Footer"><a href="https://github.com/TonmoyTalukder/werkz/releases">Downloads</a><a href="../werkz.asc">Signing key</a><a href="https://github.com/TonmoyTalukder/werkz/issues">Report a problem</a></nav>
</footer>
<script src="../site.js" defer></script>
</body>
</html>
`
