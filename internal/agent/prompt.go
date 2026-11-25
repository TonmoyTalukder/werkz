package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// systemPrompt is frozen (no dates, paths or settings) so the tools+system prefix stays
// byte-identical across turns, sessions and folders and is always a cache hit.
// Changing context goes into envContext, sent as a mid-conversation system message.
func SystemPrompt() string {
	return `You are werkz, an AI coding assistant running in the user's terminal.

You can read, search and edit files and run shell commands with the provided tools; the user approves risky actions.
Work economically: search with Grep/Glob before reading, read only the line ranges you need (Read offset/limit),
prefer small targeted edits (Edit) over rewriting files, and read a file before editing it.
Be concise: no narration of routine steps. Use GitHub-flavored markdown; keep code in fenced blocks.`
}

// envContext describes where werkz is working; it changes per session/workspace/day.
func EnvContext(dirs []string, ws string) string {
	var b strings.Builder
	b.WriteString("Environment:\n")
	if len(dirs) <= 1 {
		b.WriteString("Working directory: " + dirs[0] + "\n")
	} else {
		if ws != "" {
			b.WriteString("Workspace: " + ws + "\n")
		}
		b.WriteString("Working folders (you may read, search and edit in all of them):\n")
		for i, d := range dirs {
			if i == 0 {
				b.WriteString("- " + d + "  (main: relative paths and shell commands use this folder)\n")
			} else {
				b.WriteString("- " + d + "\n")
			}
		}
		b.WriteString("Use absolute paths for files outside the main folder. Glob and Grep without a path search every folder.\n")
	}
	b.WriteString(fmt.Sprintf("OS: %s/%s · Date: %s", runtime.GOOS, runtime.GOARCH, time.Now().Format("2006-01-02")))
	if mem := memoryFiles(dirs); mem != "" {
		b.WriteString("\n\n" + mem)
	}
	return b.String()
}

const maxMemoryBytes = 32 << 10

// memoryFiles loads each working folder's project instructions (AGENTS.md, else CLAUDE.md)
// once per session, capped at 32 KiB in total. They sit in the cached prefix after the first turn.
func memoryFiles(dirs []string) string {
	var b strings.Builder
	for _, d := range dirs {
		for _, name := range []string{"AGENTS.md", "CLAUDE.md", filepath.Join(".claude", "CLAUDE.md")} {
			data, err := os.ReadFile(filepath.Join(d, name))
			if err != nil || len(strings.TrimSpace(string(data))) == 0 {
				continue
			}
			room := maxMemoryBytes - b.Len()
			if room <= 200 {
				b.WriteString("\n(more project instruction files skipped: 32 KiB budget used)")
				return b.String()
			}
			text := string(data)
			if len(text) > room {
				text = text[:room] + "\n… (truncated at the 32 KiB budget; Read the file for the rest)"
			}
			fmt.Fprintf(&b, "Project instructions from %s:\n%s\n\n", filepath.Join(d, name), strings.TrimSpace(text))
			break
		}
	}
	return strings.TrimSpace(b.String())
}
