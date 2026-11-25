package tools

import (
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

// roots returns where Glob/Grep search: the explicit path, else every workspace folder.
func roots(path string, dirs []string) []string {
	if path != "" {
		return []string{path}
	}
	if len(dirs) > 1 {
		return dirs
	}
	return []string{"."}
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
