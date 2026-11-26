package web

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// dropped elements carry no readable content (or are page chrome) and are skipped with their children.
var dropped = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Noscript: true, atom.Template: true, atom.Svg: true,
	atom.Canvas: true, atom.Iframe: true, atom.Object: true, atom.Embed: true, atom.Nav: true,
	atom.Header: true, atom.Footer: true, atom.Aside: true, atom.Form: true, atom.Button: true,
	atom.Select: true, atom.Input: true, atom.Dialog: true, atom.Head: true,
}

// blocks are separated from their neighbours by a blank line.
var blocks = map[atom.Atom]bool{
	atom.P: true, atom.Div: true, atom.Section: true, atom.Article: true, atom.Main: true,
	atom.Body: true, atom.Html: true, atom.Figure: true, atom.Figcaption: true, atom.Details: true,
	atom.Summary: true, atom.Address: true, atom.Fieldset: true, atom.Dl: true, atom.Dt: true, atom.Dd: true,
}

// HTMLToMarkdown converts an HTML page to Markdown, keeping the readable content (headings, text, links,
// lists, code, tables) and dropping scripts and page chrome. Relative links resolve against base.
func HTMLToMarkdown(r io.Reader, base *url.URL) (title, md string, err error) {
	return HTMLToMarkdownContext(context.Background(), r, base)
}

// HTMLToMarkdownContext is HTMLToMarkdown that stops with ctx's error when ctx is cancelled. Pages
// nested too deeply for the HTML parser (over 512 levels) fall back to plain text.
func HTMLToMarkdownContext(ctx context.Context, r io.Reader, base *url.URL) (title, md string, err error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return "", "", err
	}
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return plainText(ctx, data)
	}
	if err := ctx.Err(); err != nil { // the parser itself can't be interrupted
		return "", "", err
	}
	if t := find(doc, func(n *html.Node) bool { return n.DataAtom == atom.Title }); t != nil {
		title = strings.Join(strings.Fields(textOf(t)), " ")
	}
	root := contentRoot(doc)
	c := &conv{base: base, st: &convState{ctx: ctx}}
	c.children(root)
	if c.st.err != nil {
		return "", "", c.st.err
	}
	return title, strings.TrimSpace(c.sb.String()), nil
}

// plainText is the fallback for pages the parser refuses: tag-stripped text with whitespace collapsed,
// one paragraph per block-level break, without script and style contents.
func plainText(ctx context.Context, data []byte) (title, md string, err error) {
	z := html.NewTokenizer(bytes.NewReader(data))
	var paras []string
	var words []string
	flush := func() {
		if len(words) > 0 {
			paras = append(paras, strings.Join(words, " "))
			words = words[:0]
		}
	}
	skip, inTitle := 0, false
	for n := 0; ; n++ {
		if n%1000 == 0 {
			if err := ctx.Err(); err != nil {
				return "", "", err
			}
		}
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		switch tt {
		case html.TextToken:
			f := strings.Fields(string(z.Text()))
			switch {
			case inTitle:
				title = strings.Join(append(strings.Fields(title), f...), " ")
			case skip == 0:
				words = append(words, f...)
			}
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			a := atom.Lookup(name)
			switch a {
			case atom.Script, atom.Style, atom.Noscript, atom.Template, atom.Svg:
				if tt == html.StartTagToken {
					skip++
				} else if tt == html.EndTagToken && skip > 0 {
					skip--
				}
			case atom.Title:
				inTitle = tt == html.StartTagToken
			default:
				if blocks[a] || a == atom.Br || a == atom.Li || a == atom.Tr || a == atom.Ul || a == atom.Ol ||
					a == atom.H1 || a == atom.H2 || a == atom.H3 || a == atom.H4 || a == atom.H5 || a == atom.H6 {
					flush()
				}
			}
		}
	}
	flush()
	return title, strings.Join(paras, "\n\n"), nil
}

// contentRoot prefers <main>, then a lone <article>, then <body>.
func contentRoot(doc *html.Node) *html.Node {
	if m := find(doc, func(n *html.Node) bool { return n.DataAtom == atom.Main && !skipped(n) }); m != nil {
		return m
	}
	var articles []*html.Node
	walk(doc, func(n *html.Node) bool {
		if n.DataAtom == atom.Article && !skipped(n) {
			articles = append(articles, n)
			return false
		}
		return !skipped(n)
	})
	if len(articles) == 1 {
		return articles[0]
	}
	if b := find(doc, func(n *html.Node) bool { return n.DataAtom == atom.Body }); b != nil {
		return b
	}
	return doc
}

// skipped reports whether an element is dropped by tag or marked hidden.
func skipped(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	if dropped[n.DataAtom] {
		return true
	}
	for _, a := range n.Attr {
		switch {
		case a.Key == "hidden":
			return true
		case a.Key == "aria-hidden" && a.Val == "true":
			return true
		case a.Key == "role" && a.Val == "navigation":
			return true
		}
	}
	return false
}

// walk visits n and its descendants depth-first; visit returns false to skip a subtree.
func walk(n *html.Node, visit func(*html.Node) bool) {
	if !visit(n) {
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, visit)
	}
}

func find(n *html.Node, match func(*html.Node) bool) *html.Node {
	var found *html.Node
	walk(n, func(x *html.Node) bool {
		if found != nil {
			return false
		}
		if match(x) {
			found = x
			return false
		}
		return true
	})
	return found
}

// textOf is the raw text content of a node.
func textOf(n *html.Node) string {
	var b strings.Builder
	walk(n, func(x *html.Node) bool {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
		}
		return true
	})
	return b.String()
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// conv writes Markdown. space is a pending single space, written only before the next word so that
// runs of whitespace collapse and lines never end in a space.
type conv struct {
	base  *url.URL
	sb    strings.Builder
	st    *convState // shared with sub-converters
	trail int        // newlines at the end of sb, capped at 2
	space bool
	tight bool // a list starts on the next line instead of after a blank line (inside list items)
}

// convState carries the cancellation check across a conversion and its sub-converters.
type convState struct {
	ctx   context.Context
	nodes int
	err   error
}

// w appends s, keeping the trailing-newline count current so block() is O(1).
func (c *conv) w(s string) {
	if s == "" {
		return
	}
	c.sb.WriteString(s)
	k := 0
	for k < len(s) && s[len(s)-1-k] == '\n' {
		k++
	}
	if k == len(s) {
		c.trail = min(c.trail+k, 2)
	} else {
		c.trail = min(k, 2)
	}
}

func (c *conv) atLineStart() bool { return c.sb.Len() == 0 || c.trail > 0 }

// text writes normal text with whitespace collapsed.
func (c *conv) text(s string) {
	for _, r := range s {
		if unicode.IsSpace(r) {
			c.space = true
			continue
		}
		if c.space && !c.atLineStart() {
			c.w(" ")
		}
		c.space = false
		c.w(string(r))
	}
}

// block ends the current block with a blank line.
func (c *conv) block() {
	c.space = false
	if c.sb.Len() == 0 {
		return
	}
	for c.trail < 2 {
		c.w("\n")
	}
}

// line ends the current line.
func (c *conv) line() {
	c.space = false
	if !c.atLineStart() {
		c.w("\n")
	}
}

func (c *conv) children(n *html.Node) {
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		c.node(ch)
	}
}

// sub converts n's children on their own, for constructs that post-process their content.
func (c *conv) sub(n *html.Node, tight bool) string {
	s := &conv{base: c.base, st: c.st, tight: tight}
	s.children(n)
	return strings.TrimSpace(s.sb.String())
}

// inline is sub flattened to one line.
func (c *conv) inline(n *html.Node) string {
	return strings.Join(strings.Fields(c.sub(n, false)), " ")
}

func (c *conv) node(n *html.Node) {
	if c.st.err != nil {
		return
	}
	if c.st.nodes++; c.st.nodes%1000 == 0 {
		if err := c.st.ctx.Err(); err != nil {
			c.st.err = err
			return
		}
	}
	switch n.Type {
	case html.TextNode:
		c.text(n.Data)
		return
	case html.ElementNode:
	default:
		return
	}
	if skipped(n) {
		return
	}
	switch a := n.DataAtom; {
	case a == atom.H1 || a == atom.H2 || a == atom.H3 || a == atom.H4 || a == atom.H5 || a == atom.H6:
		if t := c.inline(n); t != "" {
			c.block()
			level := int(n.Data[1] - '0') // h1…h6 (atom values aren't consecutive numbers)
			c.w(strings.Repeat("#", level) + " " + t)
			c.block()
		}
	case a == atom.Br:
		c.line()
	case a == atom.Hr:
		c.block()
		c.w("---")
		c.block()
	case a == atom.A:
		c.link(n)
	case a == atom.Img:
		c.image(n)
	case a == atom.Code:
		c.code(n)
	case a == atom.Strong || a == atom.B:
		c.emphasis(n, "**")
	case a == atom.Em || a == atom.I:
		c.emphasis(n, "*")
	case a == atom.Pre:
		c.pre(n)
	case a == atom.Ul || a == atom.Ol:
		c.list(n)
	case a == atom.Blockquote:
		c.quote(n)
	case a == atom.Table:
		c.table(n)
	case blocks[a]:
		c.block()
		c.children(n)
		c.block()
	default:
		c.children(n)
	}
}

func (c *conv) link(n *html.Node) {
	t := c.inline(n)
	if t == "" {
		return
	}
	href := strings.TrimSpace(attr(n, "href"))
	if strings.HasPrefix(href, "#") && anchorMark[strings.TrimSpace(t)] {
		return // a heading's "#" / "¶" permalink: noise in Markdown
	}
	u, err := c.resolve(href)
	if err != nil || u == "" {
		c.text(t) // no usable target: keep the words
		return
	}
	c.text("[" + t + "](" + u + ")")
}

var anchorMark = map[string]bool{"#": true, "¶": true, "§": true, "🔗": true, "link": true}

// resolve makes href absolute; javascript: links and unparsable ones give "".
func (c *conv) resolve(href string) (string, error) {
	u, err := url.Parse(href)
	if err != nil {
		return "", err
	}
	if c.base != nil {
		u = c.base.ResolveReference(u)
	}
	if strings.EqualFold(u.Scheme, "javascript") {
		return "", nil
	}
	s := u.String()
	return strings.NewReplacer("(", "%28", ")", "%29", " ", "%20").Replace(s), nil
}

func (c *conv) image(n *html.Node) {
	alt := strings.Join(strings.Fields(attr(n, "alt")), " ")
	if alt == "" {
		return
	}
	src, err := c.resolve(strings.TrimSpace(attr(n, "src")))
	if err != nil || src == "" {
		return
	}
	c.text("![" + alt + "](" + src + ")")
}

func (c *conv) code(n *html.Node) {
	t := strings.Join(strings.Fields(textOf(n)), " ")
	if t == "" {
		return
	}
	fence := "`"
	if strings.Contains(t, "`") {
		fence = "``"
	}
	c.text(fence + t + fence)
}

func (c *conv) emphasis(n *html.Node, mark string) {
	t := c.inline(n)
	if t == "" {
		return
	}
	c.text(mark + t + mark)
}

// pre writes a fenced block; the language comes from class="language-x" on the <code> child.
func (c *conv) pre(n *html.Node) {
	lang := langOf(n)
	if code := find(n, func(x *html.Node) bool { return x.DataAtom == atom.Code }); code != nil && lang == "" {
		lang = langOf(code)
	}
	body := strings.Trim(textOf(n), "\n")
	if strings.TrimSpace(body) == "" {
		return
	}
	fence := "```"
	for strings.Contains(body, fence) {
		fence += "`"
	}
	c.block()
	c.w(fence + lang + "\n" + body + "\n" + fence)
	c.block()
}

func langOf(n *html.Node) string {
	for _, cl := range strings.Fields(attr(n, "class")) {
		for _, p := range []string{"language-", "lang-"} {
			if l, ok := strings.CutPrefix(cl, p); ok {
				return l
			}
		}
	}
	return ""
}

func (c *conv) list(n *html.Node) {
	if c.tight && c.sb.Len() > 0 {
		c.line()
	} else {
		c.block()
	}
	i := 0
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.DataAtom != atom.Li || skipped(ch) {
			continue
		}
		body := c.sub(ch, true)
		if body == "" {
			continue
		}
		i++
		marker := "- "
		if n.DataAtom == atom.Ol {
			marker = strconv.Itoa(i) + ". "
		}
		c.w(prefix(body, marker, strings.Repeat(" ", len(marker))) + "\n")
	}
	c.block()
}

func (c *conv) quote(n *html.Node) {
	body := c.sub(n, false)
	if body == "" {
		return
	}
	c.block()
	c.w(prefix(body, "> ", "> "))
	c.block()
}

// prefix puts first before the first line and rest before the others (empty lines get just the trimmed rest).
func prefix(s, first, rest string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		p := rest
		if i == 0 {
			p = first
		}
		if l == "" {
			l = strings.TrimRight(p, " ")
		} else {
			l = p + l
		}
		lines[i] = l
	}
	return strings.Join(lines, "\n")
}

// table writes a GitHub pipe table; the first row is the header and cells are flattened to one line.
func (c *conv) table(n *html.Node) {
	var rows [][]string
	walk(n, func(x *html.Node) bool {
		if x != n && (skipped(x) || x.DataAtom == atom.Table) {
			return false
		}
		if x.DataAtom != atom.Tr {
			return true
		}
		var row []string
		for cell := x.FirstChild; cell != nil; cell = cell.NextSibling {
			if cell.DataAtom == atom.Td || cell.DataAtom == atom.Th {
				row = append(row, strings.ReplaceAll(c.inline(cell), "|", `\|`))
			}
		}
		if len(row) > 0 {
			rows = append(rows, row)
		}
		return false
	})
	if len(rows) == 0 {
		return
	}
	cols := 0
	for _, r := range rows {
		cols = max(cols, len(r))
	}
	line := func(r []string) string {
		for len(r) < cols {
			r = append(r, "")
		}
		return "| " + strings.Join(r, " | ") + " |"
	}
	sep := make([]string, cols)
	for i := range sep {
		sep[i] = "---"
	}
	c.block()
	c.w(line(rows[0]) + "\n" + line(sep) + "\n")
	for _, r := range rows[1:] {
		c.w(line(r) + "\n")
	}
	c.block()
}
