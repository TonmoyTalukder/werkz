package agent

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/llm"
	"github.com/TonmoyTalukder/werkz/internal/tools"
)

// Compaction replaces a long conversation with a structured summary so every later request
// re-sends far fewer tokens. The summary request re-uses the exact cached prefix (same model,
// system and tools; the instruction is appended as a new user message), so it costs a cache
// read plus the summary's output.

const compactInstruction = `Pause the task. Write a handoff summary of this conversation so the work can continue in a fresh, much shorter context. Do not call any tools; reply with the summary only.

Keep:
- the user's goals and the current task, quoting exact requirements they gave
- decisions made and the reasons for them
- files created or changed (paths) and what changed in each
- the current plan step and the next concrete action
- the last failing command and its error output, verbatim
- open questions, and anything the user asked to remember

Drop: contents of files already read (keep paths and the key facts only), passing test output, abandoned attempts, routine tool chatter.

Format as short markdown sections: Task, Decisions, Files, State, Next.`

const (
	defaultCompactAt  = 300_000 // tokens; well before the window so every call re-sends less
	hardCompactCap    = 700_000 // never postpone compaction beyond this
	debugHoldCalls    = 60      // a failing test/build holds auto-compaction for this many tool calls
	compactMinHistory = 4
)

// CompactThreshold is the context size that triggers auto-compaction for the current model.
func (e *Engine) CompactThreshold() (at, hard int64) { return CompactThreshold(e.cfg, e.lastWindow) }

// CompactThreshold is the context size that triggers auto-compaction with these settings for a
// model with this full window (0 if unknown): the /compact auto setting, but always within
// the context window (the user's limit or the model's), at 80% of it, and never later than 92%.
func CompactThreshold(cfg *config.Config, modelWindow int) (at, hard int64) {
	at = int64(defaultCompactAt)
	if cfg.CompactAt > 0 {
		at = int64(cfg.CompactAt)
	}
	hard = hardCompactCap
	if w := int64(cfg.Window(modelWindow)); w > 0 {
		at = min(at, w*8/10)
		hard = min(hard, w*92/100)
	}
	return at, max(hard, at)
}

func (e *Engine) shouldAutoCompact() bool {
	// Claude Code keeps (and compacts) its own conversation.
	if e.cfg.NoAutoCompact || e.autoCompactOff || len(e.history) < compactMinHistory || e.lastProvider == "claudecode" {
		return false
	}
	at, hard := e.CompactThreshold()
	if e.lastContext < at {
		return false
	}
	// Don't summarise away a failure that is being debugged, unless the context is near the limit.
	if e.checkFailed && e.sinceFail < debugHoldCalls && e.lastContext < hard {
		return false
	}
	return true
}

// checkCmd matches test/build/lint commands whose failure means "debugging in progress".
var checkCmd = regexp.MustCompile(`\b(go\s+(test|build|vet)|golangci-lint|vitest|jest|tsc|pytest|cargo\s+(test|build|check|clippy)|(npm|pnpm|yarn|bun)\s+(run\s+)?(test|build|lint|typecheck)|make\b|biome)\b`)

func (e *Engine) trackCheck(tc llm.ToolCall, res tools.Result) {
	if e.checkFailed {
		e.sinceFail++
	}
	if tc.Name != "Bash" {
		return
	}
	a, err := tools.ParseArgs(tc)
	if err != nil || !checkCmd.MatchString(a.Command) {
		return
	}
	e.checkFailed, e.sinceFail = res.IsError, 0
}

// Compact runs a manual /compact as its own turn.
func (e *Engine) Compact(ctx context.Context, focus string, settings config.Config) {
	e.cfg = &settings
	e.StopKeepAlive()
	e.finish(e.compact(ctx, focus, false))
}

func (e *Engine) compact(ctx context.Context, focus string, auto bool) error {
	if len(e.history) == 0 {
		return errors.New("nothing to compact yet")
	}
	instr := compactInstruction
	if focus = strings.TrimSpace(focus); focus != "" {
		instr += "\n\nThe user asked the summary to focus on: " + focus
	}
	before := e.lastContext
	saved := e.history
	e.setHistory(append(append([]llm.Msg{}, saved...), llm.Msg{Role: llm.RoleUser, Text: instr}))
	e.quiet = true
	turn, err := e.callWithFallback(ctx, SystemPrompt())
	if err != nil && ctx.Err() == nil && tooLong(err) {
		// The conversation no longer fits the model (e.g. one enormous tool result): summarise a
		// copy with oversized tool inputs and results cut down, so the session can recover.
		e.setHistory(append(shrinkForSummary(saved), llm.Msg{Role: llm.RoleUser, Text: instr}))
		turn, err = e.callWithFallback(ctx, SystemPrompt())
	}
	e.quiet = false
	if err != nil {
		e.setHistory(saved)
		return err
	}
	summary := strings.TrimSpace(turn.Text)
	if summary == "" {
		e.setHistory(saved)
		return errors.New("the model returned no summary")
	}
	text := "This session continues an earlier conversation that was compacted to save context.\n\n" +
		"## Summary of the conversation so far\n\n" + summary
	if r := gitRecap(e.dirs()); r != "" {
		text += "\n\n## Repository state now\n\n" + r
	}
	_, wsName := e.workspaceEnv()
	env := EnvContext(e.dirs(), wsName)
	e.setHistory([]llm.Msg{{Role: llm.RoleUser, Text: text}})
	e.attached = nil // the summary doesn't carry attached files
	e.memDirs = ""   // the summary drops them: send the project instructions again
	e.addMemory()
	e.appendHistory(llm.Msg{Role: llm.RoleSystem, Text: env})
	e.envSent = env
	e.lastKA = nil
	after := int64(len(text)/4 + len(env)/4 + 2500) // rough: summary + env + system/tools
	e.lastContext = after
	e.send(CompactDoneMsg{Before: before, After: after, Auto: auto})
	return nil
}

// gitRecap re-adds the facts a compacted session would otherwise spend calls rediscovering:
// branch, last commits and uncommitted files of each working folder that is a git repo.
func gitRecap(dirs []string) string {
	var b strings.Builder
	for _, d := range dirs {
		git := func(args ...string) string {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, "git", append([]string{"-C", d}, args...)...).Output()
			if err != nil {
				return ""
			}
			return strings.TrimSpace(string(out))
		}
		branch := git("rev-parse", "--abbrev-ref", "HEAD")
		if branch == "" {
			continue
		}
		fmt.Fprintf(&b, "%s (branch %s)\n", filepath.Base(d), branch)
		if log := git("log", "--oneline", "-3"); log != "" {
			b.WriteString("recent commits:\n" + log + "\n")
		}
		if st := git("status", "--short"); st != "" {
			lines := strings.Split(st, "\n")
			if len(lines) > 20 {
				lines = append(lines[:20], fmt.Sprintf("… %d more", len(lines)-20))
			}
			b.WriteString("uncommitted:\n" + strings.Join(lines, "\n") + "\n")
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// tooLong reports whether an error means the request exceeded the model's context window.
func tooLong(err error) bool {
	low := strings.ToLower(err.Error())
	for _, s := range []string{"prompt is too long", "too long", "context length", "context window",
		"maximum context", "too many tokens", "reduce the length"} {
		if strings.Contains(low, s) {
			return true
		}
	}
	return false
}

const summaryClip = 2000 // characters kept from each tool input/result when shrinking

// shrinkForSummary copies the history with tool results and tool-call inputs cut to
// summaryClip characters. Provider-native messages are dropped (rebuilt from the neutral
// form), since they would still carry the oversized content.
func shrinkForSummary(h []llm.Msg) []llm.Msg {
	clip := func(s string) string {
		if len(s) <= summaryClip {
			return s
		}
		return strings.ToValidUTF8(s[:summaryClip], "") + fmt.Sprintf("… (%d characters cut for the summary)", len(s)-summaryClip)
	}
	out := make([]llm.Msg, 0, len(h))
	for _, m := range h {
		m.ClaudeRaw, m.RespItems = nil, nil
		if m.Role == llm.RoleTool {
			m.Text = clip(m.Text)
		}
		m.Steps = clip(m.Steps)
		if len(m.ToolCalls) > 0 {
			calls := make([]llm.ToolCall, len(m.ToolCalls))
			for i, tc := range m.ToolCalls {
				if len(tc.Args) > summaryClip {
					tc.Args = `{"note":"input cut for the summary"}`
				}
				calls[i] = tc
			}
			m.ToolCalls = calls
		}
		out = append(out, m)
	}
	return out
}
