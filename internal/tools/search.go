package tools

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/textutil"
)

var skipDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true, ".venv": true, "dist": true, "build": true, "__pycache__": true}

func walk(root string, fn func(path string, d fs.DirEntry) bool) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable entries are skipped, not fatal to a search
		}
		if d.IsDir() && p != root && skipDirs[d.Name()] {
			return filepath.SkipDir
		}
		if !fn(p, d) {
			return fs.SkipAll
		}
		return nil
	})
}

// globToRegexp supports *, ?, and ** across directories.
func globToRegexp(g string) *regexp.Regexp {
	var sb strings.Builder
	sb.WriteString("^")
	for i := 0; i < len(g); i++ {
		c := g[i]
		switch {
		case c == '*' && i+1 < len(g) && g[i+1] == '*':
			i++
			if i+1 < len(g) && g[i+1] == '/' {
				i++
				sb.WriteString("(?:.*/)?")
			} else {
				sb.WriteString(".*")
			}
		case c == '*':
			sb.WriteString("[^/]*")
		case c == '?':
			sb.WriteString("[^/]")
		default:
			sb.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	sb.WriteString("$")
	re, err := regexp.Compile(sb.String())
	if err != nil {
		return regexp.MustCompile("^$")
	}
	return re
}

func globTool(a Args, dirs []string) Result {
	re := globToRegexp(a.Pattern)
	type hit struct {
		p string
		t time.Time
	}
	var hits []hit
	for _, root := range roots(a.Path, dirs) {
		walk(root, func(p string, d fs.DirEntry) bool {
			rel, _ := filepath.Rel(root, p)
			if !d.IsDir() && re.MatchString(filepath.ToSlash(rel)) {
				h := hit{p: p}
				if info, err := d.Info(); err == nil {
					h.t = info.ModTime()
				}
				hits = append(hits, h)
			}
			return len(hits) < 5000
		})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].t.After(hits[j].t) }) // recently changed first
	const maxGlob = 200
	var out []string
	for i, h := range hits {
		if i == maxGlob {
			out = append(out, fmt.Sprintf("… %d more files (narrow the pattern)", len(hits)-maxGlob))
			break
		}
		out = append(out, h.p)
	}
	return Result{Output: strings.Join(out, "\n"), Summary: fmt.Sprintf("%d files", len(hits))}
}

func grepTool(a Args, dirs []string) Result {
	re, err := regexp.Compile(a.Pattern)
	if err != nil {
		return Fail("bad regexp: %v", err)
	}
	limit := a.HeadLimit
	if limit <= 0 {
		limit = 200
	}
	mode := a.OutputMode
	if mode == "" {
		mode = "files_with_matches"
	}
	type fileHits struct {
		path  string
		lines []string
		count int
	}
	var files []fileHits
	total := 0
	for _, root := range roots(a.Path, dirs) {
		walk(root, func(p string, d fs.DirEntry) bool {
			if d.IsDir() {
				return true
			}
			if a.Glob != "" {
				if ok, _ := filepath.Match(a.Glob, d.Name()); !ok {
					return true
				}
			}
			info, err := d.Info()
			if err != nil || info.Size() > 2<<20 {
				return true
			}
			b, err := os.ReadFile(p)
			if err != nil || isBinary(b) {
				return true
			}
			fh := fileHits{path: p}
			for i, line := range strings.Split(string(b), "\n") {
				if re.MatchString(line) {
					fh.count++
					if mode == "content" && len(fh.lines) < limit {
						fh.lines = append(fh.lines, fmt.Sprintf("%s:%d: %s", p, i+1, textutil.Truncate(line, 200)))
					}
				}
			}
			if fh.count > 0 {
				files = append(files, fh)
				total += fh.count
			}
			return len(files) < 10000
		})
	}
	var out []string
	more := 0
	for _, f := range files {
		switch mode {
		case "content":
			for _, l := range f.lines {
				if len(out) >= limit {
					more++
					continue
				}
				out = append(out, l)
			}
		case "count":
			if len(out) >= limit {
				more++
				continue
			}
			out = append(out, fmt.Sprintf("%s:%d", f.path, f.count))
		default:
			if len(out) >= limit {
				more++
				continue
			}
			out = append(out, f.path)
		}
	}
	if more > 0 {
		out = append(out, fmt.Sprintf("… %d more not shown (raise head_limit or narrow the search)", more))
	}
	return Result{Output: strings.Join(out, "\n"), Summary: fmt.Sprintf("%d matches in %d files", total, len(files))}
}

func isBinary(b []byte) bool {
	n := min(len(b), 8000)
	for _, c := range b[:n] {
		if c == 0 {
			return true
		}
	}
	return false
}
