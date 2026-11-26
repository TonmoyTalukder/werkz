package web

import (
	"net/url"
	"strings"
	"testing"
)

const realisticPage = `<!doctype html>
<html><head><title>
  Widget   Guide </title><style>body{color:red}</style><script>var x = "SCRIPT_TEXT";</script></head>
<body>
<nav><a href="/home">NAVLINK</a></nav>
<header>HEADER_TEXT</header>
<div class="cookie">outside main</div>
<main>
  <h1>Widget Guide</h1>
  <p>Read the <a href="/docs/start">start   page</a>, the <a href="https://example.org/x?a=1&amp;b=2">site</a>
  or <a href="javascript:void(0)">a dead link</a>. Use <code>go test</code> with <strong>care</strong> and <em>joy</em>.<br>Second line.</p>
  <img src="/img/a.png" alt="A diagram"><img src="/img/decor.png">
  <h2>Install</h2>
  <pre><code class="language-go">func main() {

	fmt.Println("hi")
}
</code></pre>
  <ul>
    <li>one
      <ul><li>nested a</li><li>nested b</li></ul>
    </li>
    <li>two</li>
  </ul>
  <ol><li>first</li><li>second</li></ol>
  <blockquote><p>quoted text</p></blockquote>
  <table>
    <thead><tr><th>Name</th><th>Value</th></tr></thead>
    <tbody><tr><td>a|b</td><td>1</td></tr><tr><td>c</td></tr></tbody>
  </table>
  <hr>
  <button>BUTTON_TEXT</button>
  <div hidden>HIDDEN_TEXT</div>
  <p aria-hidden="true">ARIA_HIDDEN</p>
</main>
<footer>FOOTER_TEXT</footer>
</body></html>`

func TestHTMLToMarkdown(t *testing.T) {
	base, _ := url.Parse("https://example.com/guide/index.html")
	title, md, err := HTMLToMarkdown(strings.NewReader(realisticPage), base)
	if err != nil {
		t.Fatal(err)
	}
	if title != "Widget Guide" {
		t.Errorf("title = %q", title)
	}
	for _, bad := range []string{"SCRIPT_TEXT", "NAVLINK", "HEADER_TEXT", "outside main", "BUTTON_TEXT", "HIDDEN_TEXT", "ARIA_HIDDEN", "FOOTER_TEXT", "decor.png", "javascript:"} {
		if strings.Contains(md, bad) {
			t.Errorf("output should not contain %q:\n%s", bad, md)
		}
	}
	for _, want := range []string{
		"# Widget Guide\n\nRead the [start page](https://example.com/docs/start), the [site](https://example.org/x?a=1&b=2)",
		"or a dead link.",
		"`go test` with **care** and *joy*.\nSecond line.",
		"![A diagram](https://example.com/img/a.png)",
		"## Install",
		"```go\nfunc main() {\n\n\tfmt.Println(\"hi\")\n}\n```",
		"- one\n  - nested a\n  - nested b\n- two",
		"1. first\n2. second",
		"> quoted text",
		"| Name | Value |\n| --- | --- |\n| a\\|b | 1 |\n| c |  |",
		"---",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("output is missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "\n\n\n") {
		t.Errorf("more than one blank line in a row:\n%s", md)
	}
}

func TestHTMLToMarkdownFallbacks(t *testing.T) {
	// no <main>: a single <article> wins; with two, the whole body is used.
	one := `<body><p>side</p><article><p>story</p></article></body>`
	if _, md, _ := HTMLToMarkdown(strings.NewReader(one), nil); md != "story" {
		t.Errorf("single article: %q", md)
	}
	two := `<body><p>side</p><article>a</article><article>b</article></body>`
	if _, md, _ := HTMLToMarkdown(strings.NewReader(two), nil); !strings.Contains(md, "side") || !strings.Contains(md, "b") {
		t.Errorf("two articles: %q", md)
	}
}

func TestHeadingLevels(t *testing.T) {
	_, md, err := HTMLToMarkdown(strings.NewReader("<h1>A</h1><h2>B</h2><h3>C</h3><h4>D</h4><h5>E</h5><h6>F</h6>"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# A", "\n## B", "\n### C", "\n#### D", "\n##### E", "\n###### F"} {
		if !strings.Contains(md, want) {
			t.Fatalf("missing %q in:\n%s", want, md)
		}
	}
	if strings.Contains(md, "#######") {
		t.Fatalf("too many #:\n%s", md)
	}
}

func TestHeadingPermalinksDropped(t *testing.T) {
	_, md, _ := HTMLToMarkdown(strings.NewReader(`<h2 id="x">Usage<a href="#x">#</a></h2><p><a href="#x">see usage</a></p>`), nil)
	if strings.Contains(md, "[#]") || !strings.Contains(md, "## Usage") || !strings.Contains(md, "[see usage](#x)") {
		t.Fatalf("md:\n%s", md)
	}
}
