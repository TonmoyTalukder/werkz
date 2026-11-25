package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/TonmoyTalukder/werkz/internal/catalog"
	"github.com/TonmoyTalukder/werkz/internal/llm"
	"github.com/TonmoyTalukder/werkz/internal/textutil"
	"github.com/TonmoyTalukder/werkz/internal/tools"
)

// The Task tool runs a sub-agent in a fresh context with read-only tools and returns only its
// report, so broad exploration never enters (or bloats) the main conversation. It runs on the
// user's current provider (Claude Haiku 5.5 at low effort when that provider is Claude); its own
// system prompt and tool list are fixed, so repeated Task calls hit the prompt cache too.

const subAgentPrompt = `You are a read-only research sub-agent for a coding assistant. Investigate the brief using
Grep, Glob, LS and Read (search first, read only the ranges you need), then reply with a concise
report of at most ~300 words: the answer, with file paths and line numbers as evidence. Do not
include long code excerpts. Do not ask questions; if something cannot be determined, say so.`

const (
	subAgentModel     = "claude-haiku-5-5"
	subAgentMaxSteps  = 25
	subAgentMaxReport = 8000
)

func subAgentTools() []llm.ToolDef {
	var out []llm.ToolDef
	for _, d := range tools.Defs {
		switch d.Name {
		case "Read", "LS", "Glob", "Grep":
			out = append(out, d)
		}
	}
	return out
}

func (e *Engine) runTask(ctx context.Context, tc llm.ToolCall) tools.Result {
	a, err := tools.ParseArgs(tc)
	if err != nil || strings.TrimSpace(a.Prompt) == "" {
		return tools.Fail("Task needs a prompt")
	}
	// Stay on the provider the user chose: their code goes nowhere else, and nothing is billed
	// to an account they didn't pick. On Claude, use the cheaper Haiku at low effort.
	provider, model, effort := e.cfg.Provider, e.cfg.Model, e.cfg.Effort
	if provider == "anthropic" {
		model, effort = subAgentModel, "low"
	}
	wsEnv, wsName := e.workspaceEnv()
	hist := []llm.Msg{{Role: llm.RoleUser, Text: a.Prompt}, {Role: llm.RoleSystem, Text: EnvContext(e.dirs(), wsName)}}
	defs := subAgentTools()
	env := tools.Env{Dirs: e.dirs(), Env: wsEnv} // own read tracking: it never edits
	p := e.provider(provider)
	mi := catalog.Lookup(provider, model)
	var cost float64
	steps := 0
	for ; steps < subAgentMaxSteps; steps++ {
		turn, err := p.Stream(ctx, llm.Request{Model: model, System: subAgentPrompt, History: hist, Tools: defs,
			Effort: effort, TTL1h: false, CacheKey: e.sessionID + "-task"}, llm.Handlers{
			OnText: func(string) {}, OnThinking: func(string) {}, OnToolStart: func(string) {}})
		if err != nil {
			r, _ := Classify(provider, err)
			return tools.Fail("sub-agent failed: %s", r)
		}
		c := mi.Cost(turn.Usage.In, turn.Usage.Out, turn.Usage.CacheRead, turn.Usage.CacheWrite, false, false)
		cost += c
		e.send(UsageMsg{Provider: provider, Model: model, Usage: turn.Usage, Cost: c, Sub: true})
		hist = append(hist, llm.Msg{Role: llm.RoleAssistant, Text: turn.Text, ToolCalls: turn.ToolCalls, ClaudeRaw: turn.Raw})
		if len(turn.ToolCalls) == 0 {
			report := strings.TrimSpace(turn.Text)
			if report == "" {
				return tools.Fail("sub-agent returned no report")
			}
			if len(report) > subAgentMaxReport {
				report = report[:subAgentMaxReport] + "\n… (report truncated)"
			}
			return tools.Result{Output: report, Summary: fmt.Sprintf("%d steps · %s · $%.4f", steps+1, mi.Label, cost),
				Detail: firstLines(report, 3)}
		}
		for _, call := range turn.ToolCalls {
			res := tools.Fail("tool not available to the sub-agent")
			switch call.Name {
			case "Read", "LS", "Glob", "Grep":
				res = tools.Run(ctx, call, env)
			}
			hist = append(hist, llm.Msg{Role: llm.RoleTool, ToolCallID: call.ID, ToolName: call.Name, Text: res.Output, IsError: res.IsError})
		}
		if ctx.Err() != nil {
			return tools.Fail("interrupted")
		}
	}
	return tools.Fail("sub-agent did not finish within %d steps", subAgentMaxSteps)
}

func firstLines(s string, n int) []string {
	lines := strings.Split(s, "\n")
	var out []string
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		out = append(out, textutil.Truncate(l, 140))
		if len(out) == n {
			break
		}
	}
	return out
}
