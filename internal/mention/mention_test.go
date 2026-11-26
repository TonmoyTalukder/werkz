package mention

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFind(t *testing.T) {
	cases := []struct {
		in   string
		want []string // paths
	}{
		{"look at @src/a.go please", []string{"src/a.go"}},
		{"mail me at a@b.com", nil},
		{"@a.go, then (@b.go).", []string{"a.go", "b.go"}},
		{`open @"My Docs/x.txt" now`, []string{"My Docs/x.txt"}},
		{`@My\ Docs/x.txt`, []string{"My Docs/x.txt"}},
		{"@main.go:10-40 and @x.go#L3", []string{"main.go", "x.go"}},
		{"just @", nil},
	}
	for _, c := range cases {
		got := Find(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("%q: %d mentions, want %d (%+v)", c.in, len(got), len(c.want), got)
		}
		for i := range got {
			if got[i].Path != c.want[i] {
				t.Errorf("%q: path %q, want %q", c.in, got[i].Path, c.want[i])
			}
		}
	}
	r := Find("@main.go:10-40 @x.go#L3")
	if r[0].From != 10 || r[0].To != 40 || r[1].From != 3 || r[1].To != 3 {
		t.Fatalf("ranges = %+v", r)
	}
	if s := "see @a.go."; Find(s)[0].Raw(s) != "@a.go" {
		t.Fatal("trailing period kept in the token")
	}
}

func tree(t *testing.T, root string, files ...string) {
	for _, f := range files {
		p := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestResolveSeveralFolders(t *testing.T) {
	base := t.TempDir()
	web, api := filepath.Join(base, "web"), filepath.Join(base, "api")
	tree(t, web, "src/app.ts", "README.md")
	tree(t, api, "main.go", "README.md")
	dirs := []string{web, api}
	for in, want := range map[string]string{
		"src/app.ts":     filepath.Join(web, "src/app.ts"),
		"api/main.go":    filepath.Join(api, "main.go"),
		"web/README.md":  filepath.Join(web, "README.md"),
		"api/README.md":  filepath.Join(api, "README.md"),
		"main.go":        filepath.Join(api, "main.go"), // only in one folder
		"README.md":      filepath.Join(web, "README.md"),
		"api/missing.go": "",
	} {
		got, ok := Resolve(in, dirs)
		if want == "" {
			if ok {
				t.Errorf("%s resolved to %s, want nothing", in, got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("%s -> %s (%v), want %s", in, got, ok, want)
		}
	}
	if d := Display(filepath.Join(api, "main.go"), dirs); d != "api/main.go" {
		t.Fatalf("display = %s", d)
	}
	if n := RootNames([]string{"/a/x/app", "/b/y/app"}); n[0] != "x-app" || n[1] != "y-app" {
		t.Fatalf("root names = %v", n)
	}
}

func TestSearch(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "src/auth/login.ts", "src/auth/logout.ts", "src/app.ts", "docs/login.md", "login.ts.bak",
		"node_modules/x/login.ts")
	ix := Build(context.Background(), []string{root}) // not a git repo: walked, node_modules skipped
	paths := func(es []Entry) []string {
		var out []string
		for _, e := range es {
			out = append(out, e.Path)
		}
		return out
	}
	if got := paths(ix.Search("src/au/log", 5, nil)); len(got) == 0 || got[0] != "src/auth/login.ts" {
		t.Fatalf("src/au/log -> %v", got)
	}
	if got := paths(ix.Search("login.md", 5, nil)); got[0] != "docs/login.md" {
		t.Fatalf("login.md -> %v", got)
	}
	got := paths(ix.Search("", 10, nil))
	if len(got) != 3 || got[0] != "docs" || got[1] != "src" {
		t.Fatalf("top level = %v, want folders first", got)
	}
	if got := paths(ix.Search("src/", 10, nil)); len(got) != 2 || got[0] != "src/auth" || got[1] != "src/app.ts" {
		t.Fatalf("src/ = %v", got)
	}
	for _, e := range ix.Entries {
		if filepath.Base(filepath.Dir(e.Abs)) == "x" {
			t.Fatalf("node_modules indexed: %s", e.Path)
		}
	}
	if got := paths(ix.Search("logout", 5, map[string]int{})); got[0] != "src/auth/logout.ts" {
		t.Fatalf("logout -> %v", got)
	}
}
