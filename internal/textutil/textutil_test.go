package textutil

import "testing"

// L1: escape sequences and control characters from untrusted text never reach the terminal.
func TestSanitize(t *testing.T) {
	for in, want := range map[string]string{
		"plain text\nnext\tline":              "plain text\nnext\tline",
		"copy \x1b]52;c;cm0gLXJmIH4=\x07this": "copy this",       // OSC 52 clipboard write
		"\x1b[2J\x1b[Hcleared":                "cleared",         // clear screen
		"red \x1b[31mtext\x1b[0m":             "red text",        // colours
		"over\rwrite":                         "overwrite",       // carriage return
		"bell\x07 and \u009b31m C1":           "bell and 31m C1", // BEL and a C1 CSI
		"unicode ✓ ünïcödé":                   "unicode ✓ ünïcödé",
	} {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}
