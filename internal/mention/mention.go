// Package mention handles @file mentions in prompts: finding them in text, resolving them to
// files in the working folders, and the file index the @ picker searches.
//
// Syntax: @path, @"path with spaces", with an optional line range: @path:10-40, @path:10,
// @path#L10-L40. In a workspace with several folders, a path may start with a folder's name
// (@api/src/main.go); without one it is relative to the main folder.
package mention

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Token is one @mention found in a text.
type Token struct {
	Start, End int    // byte offsets of the whole token, "@" included
	Path       string // as written, without "@", quotes or line range
	From, To   int    // line range (1-based, inclusive); 0 = whole file
}

// Raw is the token's text as written.
func (t Token) Raw(s string) string { return s[t.Start:t.End] }

// opensMention reports whether "@" may start a mention after the rune r: at the start of the
// text, after whitespace or an opening bracket or quote. So a@b.com is never a mention.
func opensMention(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '(', '[', '{', '"', '\'', '`':
		return true
	}
	return false
}

// Find returns the mentions in s, in order.
func Find(s string) []Token {
	var out []Token
	for i := 0; i < len(s); i++ {
		if s[i] != '@' {
			continue
		}
		if i > 0 {
			r, _ := utf8.DecodeLastRuneInString(s[:i])
			if !opensMention(r) {
				continue
			}
		}
		if t, ok := parseAt(s, i); ok {
			out = append(out, t)
			i = t.End - 1
		}
	}
	return out
}

// parseAt reads the mention starting at s[i] == '@'.
func parseAt(s string, i int) (Token, bool) {
	j := i + 1
	var path string
	if j < len(s) && s[j] == '"' { // @"quoted path"
		k := strings.IndexByte(s[j+1:], '"')
		if k < 0 {
			return Token{}, false
		}
		path = s[j+1 : j+1+k]
		j = j + 2 + k
		// a line range may follow the closing quote
		end := j
		for end < len(s) && !isSpace(s[end]) {
			end++
		}
		t := Token{Start: i, End: j, Path: path}
		if from, to, ok := lineRange(s[j:end]); ok {
			t.From, t.To, t.End = from, to, end
		}
		return t, path != ""
	}
	end := j
	for end < len(s) && !isSpace(s[end]) {
		if s[end] == '\\' && end+1 < len(s) && s[end+1] == ' ' { // @My\ Docs/a.txt
			end += 2
			continue
		}
		end++
	}
	raw := s[j:end]
	if raw == "" {
		return Token{}, false
	}
	t := Token{Start: i, End: end}
	// trailing sentence punctuation isn't part of the path: "look at @a.go."
	for raw != "" && strings.ContainsRune(".,;:!?)]}'\"`", rune(raw[len(raw)-1])) {
		raw = raw[:len(raw)-1]
		t.End--
	}
	if raw == "" {
		return Token{}, false
	}
	path = raw
	if k := strings.LastIndex(raw, "#L"); k > 0 {
		if from, to, ok := lineRange(raw[k:]); ok {
			path, t.From, t.To = raw[:k], from, to
		}
	} else if k := strings.LastIndexByte(raw, ':'); k > 0 {
		if from, to, ok := lineRange(raw[k:]); ok {
			path, t.From, t.To = raw[:k], from, to
		}
	}
	t.Path = strings.ReplaceAll(path, `\ `, " ")
	return t, true
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }

// lineRange parses ":10", ":10-40", "#L10" or "#L10-L40" (or "#L10-40").
func lineRange(s string) (from, to int, ok bool) {
	switch {
	case strings.HasPrefix(s, ":"):
		s = s[1:]
	case strings.HasPrefix(s, "#L"):
		s = s[2:]
	default:
		return 0, 0, false
	}
	a, b, found := strings.Cut(s, "-")
	b = strings.TrimPrefix(b, "L")
	from, err := strconv.Atoi(a)
	if err != nil || from < 1 {
		return 0, 0, false
	}
	to = from
	if found {
		if to, err = strconv.Atoi(b); err != nil || to < from {
			return 0, 0, false
		}
	}
	return from, to, true
}

// ---------------------------------------------------------------- folders

// RootNames returns the name each working folder is known by in mentions: its base name, or
// "parent-name" when two folders share a base name.
func RootNames(dirs []string) []string {
	names := make([]string, len(dirs))
	count := map[string]int{}
	for i, d := range dirs {
		names[i] = filepath.Base(d)
		count[names[i]]++
	}
	for i, d := range dirs {
		if count[names[i]] > 1 {
			names[i] = filepath.Base(filepath.Dir(d)) + "-" + filepath.Base(d)
		}
	}
	return names
}

// Resolve turns a mentioned path into an absolute path. In a workspace with several folders a
// leading folder name picks that folder; otherwise the path is relative to the main folder
// (or, if it isn't there, to the one other folder that has it). Absolute and ~ paths are taken
// as they are. ok is false when nothing exists at the path.
func Resolve(path string, dirs []string) (abs string, ok bool) {
	if path == "" {
		return "", false
	}
	if strings.HasPrefix(path, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(h, path[2:])
		}
	}
	if filepath.IsAbs(path) {
		_, err := os.Stat(path)
		return filepath.Clean(path), err == nil
	}
	if len(dirs) == 0 {
		wd, _ := os.Getwd()
		dirs = []string{wd}
	}
	clean := filepath.Clean(filepath.FromSlash(strings.TrimSuffix(path, "/")))
	if len(dirs) > 1 {
		first, rest, _ := strings.Cut(filepath.ToSlash(clean), "/")
		for i, name := range RootNames(dirs) {
			if name == first {
				p := filepath.Join(dirs[i], filepath.FromSlash(rest))
				if exists(p) {
					return p, true
				}
			}
		}
	}
	main := filepath.Join(dirs[0], clean)
	if exists(main) {
		return main, true
	}
	var found []string
	for _, d := range dirs[1:] {
		if p := filepath.Join(d, clean); exists(p) {
			found = append(found, p)
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	return main, false
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// Display is how a file is written in a mention: relative to its folder, prefixed with the
// folder's name when there are several folders. Paths with spaces are quoted.
func Display(abs string, dirs []string) string {
	rel := abs
	names := RootNames(dirs)
	for i, d := range dirs {
		if r, err := filepath.Rel(d, abs); err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			rel = filepath.ToSlash(r)
			if len(dirs) > 1 {
				rel = names[i] + "/" + rel
			}
			break
		}
	}
	return rel
}

// Quote returns the mention text for a path: @path, or @"path" when it contains a space.
func Quote(path string) string {
	if strings.ContainsAny(path, " \t") {
		return `@"` + path + `"`
	}
	return "@" + path
}
