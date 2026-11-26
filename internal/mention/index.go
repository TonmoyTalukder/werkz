package mention

import (
	"bytes"
	"context"
	"io/fs"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Entry is a file or folder the @ picker can offer.
type Entry struct {
	Path string // as inserted after "@": relative, folder-name prefix when several folders
	Abs  string
	Dir  bool
	base string // lower-case last element
	low  string // lower-case Path
}

// Index is a snapshot of the files in the working folders.
type Index struct {
	Dirs    []string
	Entries []Entry
	Built   time.Time
	byDir   map[string][]int // parent path -> children
}

const maxEntries = 100_000

// skipDirs are never walked outside git (inside git, .gitignore decides).
var skipDirs = map[string]bool{".git": true, "node_modules": true, ".venv": true, "venv": true,
	"__pycache__": true, ".next": true, ".cache": true, "target": true, "dist": true, "build": true,
	".idea": true, ".gradle": true, ".terraform": true, "vendor": true}

// Build lists the files in dirs: git's view of each repository (tracked plus untracked files
// that aren't ignored), or a capped walk of a folder that isn't one. It never follows symlinked
// folders and never enters .git.
func Build(ctx context.Context, dirs []string) *Index {
	ix := &Index{Dirs: append([]string(nil), dirs...), Built: time.Now(), byDir: map[string][]int{}}
	names := RootNames(dirs)
	seenDir := map[string]bool{}
	add := func(p, abs string, dir bool) {
		if len(ix.Entries) >= maxEntries {
			return
		}
		low := strings.ToLower(p)
		ix.Entries = append(ix.Entries, Entry{Path: p, Abs: abs, Dir: dir, base: path.Base(low), low: low})
		parent := path.Dir(p)
		if parent == "." {
			parent = ""
		}
		ix.byDir[parent] = append(ix.byDir[parent], len(ix.Entries)-1)
	}
	for i, root := range dirs {
		prefix := ""
		if len(dirs) > 1 {
			prefix = names[i] + "/"
			add(names[i], root, true)
		}
		files := gitFiles(ctx, root)
		if files == nil {
			files = walkFiles(ctx, root)
		}
		for _, f := range files {
			// the folders above each file
			for d := path.Dir(f); d != "." && d != "/"; d = path.Dir(d) {
				key := prefix + d
				if seenDir[key] {
					break
				}
				seenDir[key] = true
				add(key, filepath.Join(root, filepath.FromSlash(d)), true)
			}
			add(prefix+f, filepath.Join(root, filepath.FromSlash(f)), false)
		}
	}
	return ix
}

// gitFiles lists a repository's files relative to root; nil when root isn't in a git repo.
func gitFiles(ctx context.Context, root string) []string {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-co", "--exclude-standard", "-z")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var files []string
	for _, f := range bytes.Split(out, []byte{0}) {
		if len(f) > 0 {
			files = append(files, string(f))
		}
	}
	if files == nil {
		files = []string{} // an empty repository is still a repository
	}
	return files
}

func walkFiles(ctx context.Context, root string) []string {
	var files []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil || len(files) >= maxEntries {
			return filepath.SkipAll
		}
		if d.IsDir() {
			if p != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 || d.Type().IsRegular() {
			if r, err := filepath.Rel(root, p); err == nil {
				files = append(files, filepath.ToSlash(r))
			}
		}
		return nil
	})
	return files
}

// Search returns up to limit entries for the text typed after "@", best first.
//
//   - empty: the top level of the folders;
//   - ending in "/" at a folder: that folder's contents, folders first;
//   - otherwise a fuzzy match, where "/" in the query matches folder boundaries, so
//     "src/au/log" finds src/auth/login.ts. Exact and prefix matches of the file name rank
//     first, then shorter paths.
func (ix *Index) Search(q string, limit int, boost map[string]int) []Entry {
	if ix == nil {
		return nil
	}
	if q == "" || strings.HasSuffix(q, "/") {
		dir := strings.TrimSuffix(q, "/")
		if kids, ok := ix.byDir[dir]; ok {
			out := make([]Entry, 0, len(kids))
			for _, i := range kids {
				out = append(out, ix.Entries[i])
			}
			sort.SliceStable(out, func(a, b int) bool {
				if out[a].Dir != out[b].Dir {
					return out[a].Dir
				}
				return out[a].low < out[b].low
			})
			if len(out) > limit {
				out = out[:limit]
			}
			return out
		}
		if q == "" {
			return nil
		}
	}
	lq := strings.ToLower(q)
	qbase := path.Base(strings.TrimSuffix(lq, "/"))
	type hit struct {
		i     int
		score int
	}
	var hits []hit
	for i := range ix.Entries {
		e := &ix.Entries[i]
		s := score(e, lq, qbase)
		if s <= 0 {
			continue
		}
		s += boost[e.Path]
		hits = append(hits, hit{i, s})
	}
	sort.Slice(hits, func(a, b int) bool {
		if hits[a].score != hits[b].score {
			return hits[a].score > hits[b].score
		}
		pa, pb := ix.Entries[hits[a].i].Path, ix.Entries[hits[b].i].Path
		if len(pa) != len(pb) {
			return len(pa) < len(pb)
		}
		return pa < pb
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]Entry, len(hits))
	for k, h := range hits {
		out[k] = ix.Entries[h.i]
	}
	return out
}

func score(e *Entry, q, qbase string) int {
	s := 0
	switch {
	case e.base == qbase && (q == qbase || strings.HasSuffix(e.low, q)):
		s = 1000
	case e.low == q:
		s = 1000
	case strings.HasPrefix(e.base, qbase) && strings.Contains(e.low, strings.TrimSuffix(q, qbase)):
		s = 800
	case strings.HasPrefix(e.low, q):
		s = 700
	case strings.Contains(e.base, q):
		s = 600
	case strings.Contains(e.low, q):
		s = 500
	default:
		s = fuzzy(e.low, q)
		if s <= 0 {
			return 0
		}
	}
	if e.Dir {
		s -= 5 // files first on a tie, unless a folder is asked for
	}
	return s
}

// fuzzy scores q as a subsequence of p: consecutive runs and matches at the start of a path
// element score more; "/" in q must match a "/" in p. 0 = no match.
func fuzzy(p, q string) int {
	s, pi, run := 100, 0, 0
	for qi := 0; qi < len(q); qi++ {
		c := q[qi]
		found := false
		for ; pi < len(p); pi++ {
			if p[pi] == c {
				found = true
				break
			}
			if c != '/' && p[pi] == '/' && qi > 0 && q[qi-1] != '/' {
				// leaving a path element mid-word is allowed, at a cost
				s -= 2
			}
			run = 0
		}
		if !found {
			return 0
		}
		if pi == 0 || p[pi-1] == '/' || p[pi-1] == '_' || p[pi-1] == '-' || p[pi-1] == '.' {
			s += 8
		}
		run++
		s += run * 3
		pi++
	}
	s -= len(p) / 8
	return min(max(s, 1), 450) // below every substring match
}
