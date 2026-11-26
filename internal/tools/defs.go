// Package tools implements the tools the model can call (Read, Write, Edit, Bash,
// LS, Glob, Grep) with permission rules, output trimming and read/edit guards.
package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/llm"
	"github.com/TonmoyTalukder/werkz/internal/textutil"
)

type Result struct {
	Output  string   // sent to the model
	Summary string   // one line for the UI
	Detail  []string // optional extra UI lines
	IsError bool
}

const maxOut = 30_000

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func num(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }

var Defs = []llm.ToolDef{
	{Name: "Read", Description: "Read a text file. Returns numbered lines (max 2000 per call; long lines are cut). For big files read only the range you need with offset/limit, or Grep first.",
		Properties: map[string]any{"path": str("File path"), "offset": num("1-based start line (optional)"), "limit": num("Max lines (optional, default 2000)")},
		Required:   []string{"path"}},
	{Name: "Write", Description: "Create a new file, or overwrite one you have already Read. Prefer Edit for changes to existing files.",
		Properties: map[string]any{"path": str("File path"), "content": str("Full file content")},
		Required:   []string{"path", "content"}},
	{Name: "Edit", Description: "Replace an exact string in a file you have Read. old_string must match exactly and be unique unless replace_all is true.",
		Properties: map[string]any{"path": str("File path"), "old_string": str("Exact text to replace"),
			"new_string": str("Replacement text"), "replace_all": map[string]any{"type": "boolean", "description": "Replace every occurrence"}},
		Required: []string{"path", "old_string", "new_string"}},
	{Name: "Bash", Description: "Run a shell command in the working directory and return stdout/stderr.",
		Properties: map[string]any{"command": str("Shell command"), "timeout_sec": num("Timeout in seconds (default 120, max 600)")},
		Required:   []string{"command"}},
	{Name: "Task", Description: "Delegate a self-contained research task (find where something is implemented, survey many files, answer a question about the codebase) to a sub-agent with read-only tools. It returns only a short report, so the files it reads never enter this conversation. Use it for broad searches; give it a complete brief.",
		Properties: map[string]any{"description": str("3-6 word label"), "prompt": str("The full brief: what to find out and what to report back")},
		Required:   []string{"description", "prompt"}},
	{Name: "LS", Description: "List a directory.",
		Properties: map[string]any{"path": str("Directory (default .)")}},
	{Name: "Glob", Description: "Find files by glob pattern, e.g. **/*.go or src/*.ts; newest first, max 200. Without path it searches every working folder.",
		Properties: map[string]any{"pattern": str("Glob pattern"), "path": str("Root directory (default .)")},
		Required:   []string{"pattern"}},
	{Name: "Grep", Description: "Search file contents with a regular expression. output_mode: files_with_matches (default, file paths only), content (path:line: text), or count. Without path it searches every working folder.",
		Properties: map[string]any{"pattern": str("Go regular expression"), "path": str("File or directory (default .)"), "glob": str("Only files matching this filename glob, e.g. *.go"),
			"output_mode": map[string]any{"type": "string", "enum": []string{"files_with_matches", "content", "count"}, "description": "Default files_with_matches"},
			"head_limit":  num("Max results (default 200)")},
		Required: []string{"pattern"}},
}

// needsPermission returns whether the tool needs approval under the mode.
// kind "plan" means it is blocked outright.
func NeedsPermission(tool, mode string) (bool, string) {
	readOnly := tool == "Read" || tool == "LS" || tool == "Glob" || tool == "Grep" || tool == "Task"
	if readOnly {
		return false, ""
	}
	switch mode {
	case config.ModePlan:
		return true, "plan"
	case config.ModeAuto, config.ModeBypass:
		return false, ""
	case config.ModeAcceptEdits:
		return tool == "Bash", "ask"
	}
	return true, "ask"
}

type Args struct {
	Path       string `json:"path"`
	Offset     int    `json:"offset"`
	Limit      int    `json:"limit"`
	Content    string `json:"content"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
	Command    string `json:"command"`
	TimeoutSec int    `json:"timeout_sec"`
	Prompt     string `json:"prompt"`
	Desc       string `json:"description"`
	Pattern    string `json:"pattern"`
	Glob       string `json:"glob"`
	OutputMode string `json:"output_mode"`
	HeadLimit  int    `json:"head_limit"`
}

func ParseArgs(tc llm.ToolCall) (Args, error) {
	var a Args
	err := json.Unmarshal([]byte(tc.Args), &a)
	return a, err
}

func Summary(tc llm.ToolCall) string {
	a, _ := ParseArgs(tc)
	switch tc.Name {
	case "Read", "Write", "Edit":
		return RelPath(a.Path)
	case "Bash":
		return textutil.Truncate(strings.ReplaceAll(a.Command, "\n", " ⏎ "), 90)
	case "LS":
		if a.Path == "" {
			return "."
		}
		return RelPath(a.Path)
	case "Task":
		return textutil.Truncate(a.Desc, 60)
	case "Glob", "Grep":
		s := a.Pattern
		if a.Path != "" {
			s += " in " + RelPath(a.Path)
		}
		return textutil.Truncate(s, 90)
	}
	return textutil.Truncate(tc.Args, 90)
}

// previewTool renders what a risky tool is about to do (shown in the permission dialog).
func Preview(tc llm.ToolCall) []string {
	a, err := ParseArgs(tc)
	if err != nil {
		return nil
	}
	switch tc.Name {
	case "Edit":
		return DiffLines(a.OldString, a.NewString, 14)
	case "Write":
		lines := strings.Split(a.Content, "\n")
		out := []string{}
		for i, l := range lines {
			if i >= 10 {
				out = append(out, fmt.Sprintf("… %d more lines", len(lines)-10))
				break
			}
			out = append(out, "+ "+l)
		}
		return out
	case "Bash":
		return strings.Split(a.Command, "\n")
	}
	return nil
}

// DiffLines renders an edit as a line diff: only changed lines, as "- " and "+ ", with one
// unchanged line of context ("  ") on each side, cut to limit lines.
func DiffLines(oldS, newS string, limit int) []string {
	out, _, _ := lineDiff(oldS, newS)
	if len(out) > limit {
		n := len(out) - limit
		out = append(out[:limit], fmt.Sprintf("… %d more lines", n))
	}
	return out
}

// DiffStat counts the lines an edit adds and removes.
func DiffStat(oldS, newS string) (added, removed int) {
	_, added, removed = lineDiff(oldS, newS)
	return added, removed
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func lineDiff(oldS, newS string) (out []string, added, removed int) {
	a, b := splitLines(oldS), splitLines(newS)
	// common prefix and suffix are unchanged
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]
	if pre > 0 {
		out = append(out, "  "+a[pre-1])
	}
	if len(ma)*len(mb) <= 40_000 {
		// longest common subsequence over the changed middle
		l := make([][]int, len(ma)+1)
		for i := range l {
			l[i] = make([]int, len(mb)+1)
		}
		for i := len(ma) - 1; i >= 0; i-- {
			for j := len(mb) - 1; j >= 0; j-- {
				if ma[i] == mb[j] {
					l[i][j] = l[i+1][j+1] + 1
				} else {
					l[i][j] = max(l[i+1][j], l[i][j+1])
				}
			}
		}
		i, j := 0, 0
		for i < len(ma) || j < len(mb) {
			switch {
			case i < len(ma) && j < len(mb) && ma[i] == mb[j]:
				out = append(out, "  "+ma[i])
				i, j = i+1, j+1
			case j < len(mb) && (i == len(ma) || l[i][j+1] >= l[i+1][j]):
				out = append(out, "+ "+mb[j])
				added++
				j++
			default:
				out = append(out, "- "+ma[i])
				removed++
				i++
			}
		}
	} else {
		for _, x := range ma {
			out = append(out, "- "+x)
		}
		for _, x := range mb {
			out = append(out, "+ "+x)
		}
		removed, added = len(ma), len(mb)
	}
	if suf > 0 {
		out = append(out, "  "+a[len(a)-suf])
	}
	return out, added, removed
}

func Fail(format string, args ...any) Result {
	msg := fmt.Sprintf(format, args...)
	return Result{Output: "Error: " + msg, Summary: msg, IsError: true}
}
