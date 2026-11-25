package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ---------------------------------------------------------------- output trimming

// Noise from known test runners and linters that the model never needs: passing tests,
// progress lines, coverage. Unknown lines are always kept, as is the exit code.
var trimProfiles = []struct {
	name  string
	match *regexp.Regexp // command matcher
	drop  *regexp.Regexp // line matcher
}{
	{"go test", regexp.MustCompile(`\bgo\s+test\b`),
		regexp.MustCompile(`^(ok\s|\?\s|=== (RUN|PAUSE|CONT|NAME)\s|\s*--- (PASS|SKIP):|PASS$|coverage:)`)},
	{"js tests", regexp.MustCompile(`\b(vitest|jest|npm\s+(run\s+)?test|pnpm\s+(run\s+)?test|yarn\s+test|bun\s+test)\b`),
		regexp.MustCompile(`^\s*(✓|√|✔|PASS\s)|^\s*$`)},
	{"pytest", regexp.MustCompile(`\bpytest\b`),
		regexp.MustCompile(`(PASSED|\s\.+\s*\[\s*\d+%\])\s*$`)},
	{"cargo test", regexp.MustCompile(`\bcargo\s+test\b`),
		regexp.MustCompile(`^test .* \.\.\. ok$`)},
}

// verbose or machine-readable output was asked for explicitly: leave it alone
var noTrim = regexp.MustCompile(`(^|\s)(-v|--verbose|-json|--json|--reporter[= ]?verbose)(\s|$)|NO_TRIM=1|\|`)

func trimOutput(command, out string) string {
	if noTrim.MatchString(command) {
		return out
	}
	for _, p := range trimProfiles {
		if !p.match.MatchString(command) {
			continue
		}
		lines := strings.Split(out, "\n")
		kept := lines[:0]
		dropped := 0
		for _, l := range lines {
			if p.drop.MatchString(l) {
				dropped++
				continue
			}
			kept = append(kept, l)
		}
		if dropped == 0 {
			return out
		}
		return strings.Join(kept, "\n") + fmt.Sprintf("\n[werkz trim: hid %d passing/progress lines from %s output; rerun with NO_TRIM=1 to see all]", dropped, p.name)
	}
	return out
}

// spillOutput saves oversized command output to ~/.cache/werkz/outputs and returns the path.
func spillOutput(out string) string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	dir = filepath.Join(dir, "werkz", "outputs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	cleanSpills(dir)
	p := filepath.Join(dir, fmt.Sprintf("bash-%s.txt", time.Now().Format("20060102-150405.000")))
	if os.WriteFile(p, []byte(out), 0o600) != nil {
		return ""
	}
	return p
}

// cleanSpills removes saved outputs older than 7 days.
func cleanSpills(dir string) {
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > 7*24*time.Hour {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
