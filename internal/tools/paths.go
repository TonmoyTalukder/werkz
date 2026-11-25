package tools

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func RelPath(p string) string {
	if wd, err := os.Getwd(); err == nil {
		if r, err := filepath.Rel(wd, p); err == nil && !strings.HasPrefix(r, "..") {
			return r
		}
	}
	return p
}

func Expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, p[2:])
	}
	return p
}

func absPath(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}

// errHomeSearch explains why a search without a path doesn't crawl the home folder.
var errHomeSearch = errors.New("werkz is running in your home folder, which isn't searched as a whole; " +
	"pass a path, or open a project with /workspace or /add-dir")

// roots returns where Glob/Grep search: the explicit path, else every working folder except
// the home folder itself (which is never treated as a project).
func roots(path string, dirs []string) ([]string, error) {
	if path != "" {
		return []string{path}, nil
	}
	if len(dirs) == 0 {
		dirs = []string{"."}
	}
	var out []string
	for _, d := range dirs {
		if !isHome(d) {
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		return nil, errHomeSearch
	}
	return out, nil
}

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return resolvePath(h)
}

func isHome(d string) bool { h := homeDir(); return h != "" && resolvePath(d) == h }

// sensitive are credential locations (relative to the home folder) that the file tools never
// open, whatever the permission mode: API keys (including werkz's own), SSH and cloud keys.
var sensitive = []string{
	".ssh", ".gnupg", ".aws", ".azure", ".kube", ".docker/config.json", ".netrc", ".npmrc",
	".pypirc", ".git-credentials", ".config/werkz", ".config/gcloud", ".config/gh",
}

// Sensitive reports whether path (after resolving symlinks) is a credential location.
func Sensitive(path string) bool {
	if path == "" {
		return false
	}
	h := homeDir()
	if h == "" {
		return false
	}
	p := resolvePath(Expand(path))
	for _, s := range sensitive {
		if within(filepath.Join(h, s), p) {
			return true
		}
	}
	return false
}

// InsideDirs reports whether path lies in one of dirs (or the current folder when dirs is
// empty). Both sides are resolved through symlinks, so a link inside a working folder that
// points elsewhere (e.g. docs -> ~) doesn't count as inside.
func InsideDirs(path string, dirs []string) bool {
	if path == "" {
		return true
	}
	target := resolvePath(Expand(path))
	if len(dirs) == 0 {
		wd, _ := os.Getwd()
		dirs = []string{wd}
	}
	for _, d := range dirs {
		if isHome(d) {
			continue // the home folder is never a project: nothing counts as inside it
		}
		if within(resolvePath(d), target) {
			return true
		}
	}
	return false
}

func within(dir, p string) bool {
	r, err := filepath.Rel(dir, p)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

// resolvePath returns the absolute path with symlinks resolved. For a path that doesn't exist
// yet (a new file), the deepest existing ancestor is resolved and the rest appended.
func resolvePath(p string) string {
	abs := absPath(p)
	cur, rest := abs, ""
	for {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}
