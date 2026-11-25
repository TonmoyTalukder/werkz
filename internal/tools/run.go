package tools

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/llm"
	"github.com/TonmoyTalukder/werkz/internal/textutil"
)

// Env is the folder context tools run in: Dirs[0] is the main folder.
type Env struct {
	Dirs  []string
	Env   []string     // extra KEY=VALUE pairs for shell commands
	Reads *ReadTracker // files the model has read (stale-edit guard); nil disables the guard
}

func Run(ctx context.Context, tc llm.ToolCall, env Env) Result {
	a, err := ParseArgs(tc)
	if err != nil {
		return Fail("invalid tool input JSON: %v", err)
	}
	a.Path = Expand(a.Path)
	switch tc.Name {
	case "Read":
		if a.Path == "" {
			return Fail("path is required")
		}
		res := readFile(a)
		if !res.IsError {
			env.Reads.mark(a.Path)
		}
		return res
	case "Write":
		if a.Path == "" {
			return Fail("path is required")
		}
		if err := env.Reads.check(a.Path); err != nil {
			return Fail("%v", err)
		}
		if err := os.MkdirAll(filepath.Dir(a.Path), 0o755); err != nil {
			return Fail("%v", err)
		}
		_, statErr := os.Stat(a.Path)
		if err := os.WriteFile(a.Path, []byte(a.Content), 0o644); err != nil {
			return Fail("%v", err)
		}
		env.Reads.mark(a.Path)
		n := strings.Count(a.Content, "\n") + 1
		verb := "Updated"
		if statErr != nil {
			verb = "Created"
		}
		return Result{Output: fmt.Sprintf("%s %s (%d lines)", verb, a.Path, n), Summary: fmt.Sprintf("%s · %d lines", verb, n)}
	case "Edit":
		if a.Path == "" || a.OldString == "" {
			return Fail("path and old_string are required")
		}
		if err := env.Reads.check(a.Path); err != nil {
			return Fail("%v", err)
		}
		b, err := os.ReadFile(a.Path)
		if err != nil {
			return Fail("%v", err)
		}
		s := string(b)
		c := strings.Count(s, a.OldString)
		if c == 0 {
			return Fail("old_string not found in %s", RelPath(a.Path))
		}
		if c > 1 && !a.ReplaceAll {
			return Fail("old_string occurs %d times; make it unique or set replace_all", c)
		}
		if a.ReplaceAll {
			s = strings.ReplaceAll(s, a.OldString, a.NewString)
		} else {
			s = strings.Replace(s, a.OldString, a.NewString, 1)
		}
		if err := os.WriteFile(a.Path, []byte(s), 0o644); err != nil {
			return Fail("%v", err)
		}
		env.Reads.mark(a.Path)
		added := strings.Count(a.NewString, "\n") + 1
		removed := strings.Count(a.OldString, "\n") + 1
		return Result{Output: fmt.Sprintf("Edited %s (%d replacement(s))", a.Path, max(c, 1)),
			Summary: fmt.Sprintf("+%d −%d lines", added, removed), Detail: DiffLines(a.OldString, a.NewString, 8)}
	case "Bash":
		if strings.TrimSpace(a.Command) == "" {
			return Fail("command is required")
		}
		return runBash(ctx, a, env)
	case "LS":
		dir := a.Path
		if dir == "" {
			dir = "."
		}
		ents, err := os.ReadDir(dir)
		if err != nil {
			return Fail("%v", err)
		}
		var lines []string
		for _, e := range ents {
			n := e.Name()
			if e.IsDir() {
				n += "/"
			}
			lines = append(lines, n)
		}
		return Result{Output: strings.Join(lines, "\n"), Summary: fmt.Sprintf("%d entries", len(lines))}
	case "Glob":
		return globTool(a, env.Dirs)
	case "Grep":
		return grepTool(a, env.Dirs)
	}
	return Fail("unknown tool %q", tc.Name)
}

const (
	maxWholeRead = 256 << 10 // refuse whole-file reads above this; ask for a range instead
	maxLineChars = 2000
)

func readFile(a Args) Result {
	f, err := os.Open(a.Path)
	if err != nil {
		return Fail("%v", err)
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil {
		if st.IsDir() {
			return Fail("%s is a directory; use LS or Glob", RelPath(a.Path))
		}
		if st.Size() > maxWholeRead && a.Offset == 0 && a.Limit == 0 {
			return Fail("%s is %d KB; read a range with offset/limit, or Grep for what you need", RelPath(a.Path), st.Size()>>10)
		}
	}
	start := max(a.Offset, 1)
	limit := a.Limit
	if limit <= 0 {
		limit = 2000
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	var sb strings.Builder
	n, shown := 0, 0
	for sc.Scan() {
		n++
		if n < start || shown >= limit {
			continue
		}
		line := sc.Text()
		if len(line) > maxLineChars {
			line = line[:maxLineChars] + "… (line cut)"
		}
		fmt.Fprintf(&sb, "%6d\t%s\n", n, line)
		shown++
		if sb.Len() > maxOut {
			fmt.Fprintf(&sb, "… (stopped at line %d to save context; continue with offset=%d)\n", n, n+1)
			break
		}
	}
	if n >= start+limit-1 && shown == limit {
		fmt.Fprintf(&sb, "… (more lines follow; continue with offset=%d)\n", start+limit)
	}
	return Result{Output: sb.String(), Summary: fmt.Sprintf("Read %d lines", shown)}
}

func runBash(ctx context.Context, a Args, env Env) Result {
	t := a.TimeoutSec
	if t <= 0 {
		t = 120
	}
	t = min(t, 600)
	cctx, cancel := context.WithTimeout(ctx, time.Duration(t)*time.Second)
	defer cancel()
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.CommandContext(cctx, shell, "-c", a.Command)
	if len(env.Dirs) > 0 {
		cmd.Dir = env.Dirs[0]
	}
	if len(env.Env) > 0 {
		cmd.Env = append(os.Environ(), env.Env...)
	}
	// Own process group: a timeout or Esc kills the command and everything it started.
	// WaitDelay stops a background child that keeps the output pipe open (e.g. `npm run dev &`)
	// from blocking the turn. Output is capped so a runaway command can't exhaust memory.
	setProcessGroup(cmd)
	cmd.WaitDelay = 2 * time.Second
	buf := &cappedBuffer{limit: maxCapture}
	cmd.Stdout, cmd.Stderr = buf, buf
	err := cmd.Start()
	if err == nil {
		track(cmd)
		err = cmd.Wait()
		untrack(cmd)
	}
	raw := buf.String()
	if buf.dropped > 0 {
		raw += fmt.Sprintf("\n… (output capped at %d MB; %d more bytes discarded)", maxCapture>>20, buf.dropped)
	}
	s := trimOutput(a.Command, raw)
	if len(s) > maxOut {
		// Keep a short head and a longer tail (errors and summaries live at the end); the full,
		// untrimmed output goes to a file the model can Grep or Read by range.
		short := s[:2000] + fmt.Sprintf("\n… (%d characters omitted) …\n", len(s)-10000) + s[len(s)-8000:]
		if path := spillOutput(raw); path != "" {
			short += fmt.Sprintf("\n[output was %d lines / %d characters; full output saved to %s — Grep it or Read it with offset/limit]",
				strings.Count(raw, "\n")+1, len(raw), path)
		}
		s = short
	}
	code := 0
	switch {
	case errors.Is(err, exec.ErrWaitDelay):
		code = cmd.ProcessState.ExitCode()
		s += "\n(the command left background processes running; their later output isn't captured)"
	case err != nil:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			code = -1
			s += "\n" + err.Error()
		}
	}
	if errors.Is(cctx.Err(), context.DeadlineExceeded) {
		s += fmt.Sprintf("\n(timed out after %ds)", t)
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	var detail []string
	for i, l := range lines {
		if i >= 4 {
			detail = append(detail, fmt.Sprintf("… +%d lines", len(lines)-4))
			break
		}
		if strings.TrimSpace(l) != "" || i == 0 {
			detail = append(detail, textutil.Truncate(l, 160))
		}
	}
	sum := "exit 0"
	if code != 0 {
		sum = fmt.Sprintf("exit %d", code)
	}
	return Result{Output: fmt.Sprintf("exit=%d\n%s", code, s), Summary: sum, Detail: detail, IsError: code != 0}
}
