// Package textutil has small text helpers shared across packages.
package textutil

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

func Truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// Sanitize makes untrusted text (model output, tool output, file contents, provider errors)
// safe to print in a terminal: escape sequences are removed (an OSC 52 sequence could
// otherwise set the clipboard, for example), as are other control characters except newline
// and tab. Carriage returns are dropped so text can't overwrite what is already on screen.
func Sanitize(s string) string {
	clean := true
	for _, r := range s {
		if isControl(r) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	s = ansi.Strip(s)
	return strings.Map(func(r rune) rune {
		if isControl(r) {
			return -1
		}
		return r
	}, s)
}

func isControl(r rune) bool {
	return (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}

// Exact is Sanitize for text someone must read exactly before approving it (what a command runs):
// it also shows invisible format characters (bidi overrides, zero-width marks, which can reorder
// or hide text) as visible ⟨U+XXXX⟩ tags instead of letting them act.
func Exact(s string) string {
	s = Sanitize(s)
	var b strings.Builder
	for _, r := range s {
		if unicode.Is(unicode.Cf, r) {
			fmt.Fprintf(&b, "⟨U+%04X⟩", r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
