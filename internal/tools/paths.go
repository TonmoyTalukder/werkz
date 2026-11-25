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

// insideDirs reports whether path lies in one of dirs (or the current folder when dirs is empty).
func InsideDirs(path string, dirs []string) bool {
	if path == "" {
		return true
	}
	abs, err := filepath.Abs(Expand(path))
	if err != nil {
		return false
	}
	if len(dirs) == 0 {
		wd, _ := os.Getwd()
		dirs = []string{wd}
	}
	for _, d := range dirs {
		if r, err := filepath.Rel(d, abs); err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
