// Package agent runs the agent loop: model calls, tools, permissions, provider
// fallback, compaction, the Task sub-agent and prompt-cache keep-alive.
package agent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/catalog"
	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/llm"
	"github.com/TonmoyTalukder/werkz/internal/tools"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

const (
	maxIterations = 40
	maxRepeats    = 5 // identical consecutive tool calls before stopping (loop detection)
)

type Engine struct {
	sharedState
	cfg      *config.Config
	send     func(any)
	history  []llm.Msg         // conversation; written only by the engine goroutine (under mu)
	disabled map[string]string // providers down for the session (no credits / bad key)
	allow    map[string]bool   // tools approved for the session

	envSent string // last environment context sent to the model
	memDirs string // working folders whose project instruction files were last sent

	quiet          bool                                             // don't stream text to the UI (compaction)
	providerFor    func(cfg *config.Config, id string) llm.Provider // nil = real clients
	turnPin        *attempt                                         // model switched to this turn after a failure (nil = use the chain)
	autoCompactOff bool                                             // auto-compaction failed this session; don't retry every turn
	reads          *tools.ReadTracker
	sessionID      string // prompt_cache_key for OpenAI (routes requests to the same cache)
	lastContext    int64  // tokens in the last request (input incl. cache + output)
	lastWindow     int    // context window of the model used last
	checkFailed    bool   // the latest test/build/lint command failed (holds auto-compaction)
	sinceFail      int    // tool calls since that failure

	// prompt-cache keep-alive for the last Claude request
	lastKA     func(context.Context) (llm.Usage, error)
	lastReqAt  time.Time
	lastPrefix int64
	lastMI     catalog.ModelInfo
	lastFast   bool
	clientsMu  sync.Mutex
	clients    map[string]llm.Provider // one client per provider+key+endpoint (connection reuse)
	kaMu       sync.Mutex              // guards kaCancel (the UI may stop the keep-alive at any time)
	kaCancel   context.CancelFunc

	fbTurn    map[string]bool // fallback approved for the current message
	fbSession map[string]bool // fallback approved for the whole session

	// Optional workspace: several folders (dirList[0] is the main one) and extra env vars.
	// Set by the UI, read by the engine: always accessed under mu.
	dirList []string
	env     []string
	wsName  string
}

func (e *Engine) toolEnv() tools.Env {
	env, _ := e.workspaceEnv()
	return tools.Env{Dirs: e.dirs(), Env: env, Reads: e.reads}
}

func New(cfg *config.Config, send func(any)) *Engine {
	return &Engine{cfg: cfg, send: send, disabled: map[string]string{}, allow: map[string]bool{},
		fbTurn: map[string]bool{}, fbSession: map[string]bool{}, reads: tools.NewReadTracker(),
		sessionID: fmt.Sprintf("werkz-%d", time.Now().UnixNano())}
}

// Reset clears the conversation (and stops keeping its cache warm).
func (e *Engine) Reset() {
	e.StopKeepAlive()
	e.setHistory(nil)
	e.envSent, e.memDirs, e.lastKA = "", "", nil
	e.reads = tools.NewReadTracker()
}

// finish ends a turn. The keep-alive is set up before the UI hears the turn is done, so the
// UI can never interleave with it (e.g. /clear or a queued message stopping it).
func (e *Engine) finish(err error) {
	e.startKeepAlive()
	e.send(TurnDoneMsg{Err: err})
}

// Run handles one user message: model calls, tool loop and provider fallback.
// Run handles one user message. settings is a snapshot of the configuration taken by the
// caller, so the UI can keep editing its own copy while the turn runs.
func (e *Engine) Run(ctx context.Context, userText string, settings config.Config) {
	e.cfg = &settings
	e.StopKeepAlive()
	// Compact (if due) before adding the new message, so it reaches the model word for word.
	if e.shouldAutoCompact() && !e.autoCompact(ctx, "") {
		return
	}
	e.addMemory()
	e.appendHistory(llm.Msg{Role: llm.RoleUser, Text: userText})
	_, wsName := e.workspaceEnv()
	if env := EnvContext(e.dirs(), wsName); env != e.envSent {
		e.appendHistory(llm.Msg{Role: llm.RoleSystem, Text: env})
		e.envSent = env
	}
	e.fbTurn = map[string]bool{}
	e.turnPin = nil
	sys := SystemPrompt()
	lastSig, repeats, loopStop := "", 0, false
	for i := 0; i < maxIterations; i++ {
		// Mid-turn compaction keeps the current request verbatim after the summary.
		if i > 0 && e.shouldAutoCompact() && !e.autoCompact(ctx, userText) {
			return
		}
		turn, err := e.callWithFallback(ctx, sys)
		if err != nil {
			e.finish(err)
			return
		}
		e.appendHistory(llm.Msg{Role: llm.RoleAssistant, Text: turn.Text, ToolCalls: turn.ToolCalls, ClaudeRaw: turn.Raw})
		switch turn.StopReason {
		case string(anthropic.BetaStopReasonRefusal):
			e.send(NoticeMsg{"warn", "The model declined this request (refusal)."})
			e.finish(nil)
			return
		case string(anthropic.BetaStopReasonMaxTokens), "length":
			if len(turn.ToolCalls) > 0 {
				// tool input may be truncated: do not run it
				for _, tc := range turn.ToolCalls {
					e.appendHistory(llm.Msg{Role: llm.RoleTool, ToolCallID: tc.ID, ToolName: tc.Name, IsError: true,
						Text: "Output limit reached before this tool call was complete; it was not run. Retry with smaller steps."})
				}
				continue
			}
			e.send(NoticeMsg{"warn", "Response hit the output limit."})
		}
		if len(turn.ToolCalls) == 0 {
			e.finish(nil)
			return
		}
		for _, tc := range turn.ToolCalls {
			if sig := tc.Name + "\x00" + tc.Args; sig == lastSig {
				repeats++
			} else {
				lastSig, repeats = sig, 1
			}
			if repeats >= maxRepeats {
				e.appendHistory(llm.Msg{Role: llm.RoleTool, ToolCallID: tc.ID, ToolName: tc.Name, IsError: true,
					Text: "Stopped: this exact tool call was repeated several times in a row."})
				e.send(NoticeMsg{"warn", fmt.Sprintf("Stopped a loop: %s was called %d times in a row with the same input.", tc.Name, repeats)})
				loopStop = true
				continue
			}
			if ctx.Err() != nil {
				e.appendHistory(llm.Msg{Role: llm.RoleTool, ToolCallID: tc.ID, ToolName: tc.Name, IsError: true, Text: "Interrupted by user."})
				continue
			}
			res := e.execTool(ctx, tc)
			e.appendHistory(llm.Msg{Role: llm.RoleTool, ToolCallID: tc.ID, ToolName: tc.Name, Text: res.Output, IsError: res.IsError})
		}
		if ctx.Err() != nil {
			e.finish(context.Canceled)
			return
		}
		if loopStop {
			e.finish(nil)
			return
		}
	}
	e.send(NoticeMsg{"warn", fmt.Sprintf("Stopped after %d tool iterations.", maxIterations)})
	e.finish(nil)
}

func (e *Engine) callWithFallback(ctx context.Context, sys string) (*llm.Turn, error) {
	chain := e.chain()
	if len(chain) == 0 {
		return nil, errors.New("no provider available — run /login to add an API key, or start Ollama")
	}
	tried := map[string]bool{}
	a := chain[0]
	if e.turnPin != nil {
		a = *e.turnPin // already switched this turn: don't retry the failing model every step
	} else if why := e.unusable(a.provider); why != "" {
		// An unusable main model is a failure like any other: the fallback mode (off by default)
		// decides whether to switch, so a disabled provider never silently routes elsewhere.
		tried[a.key()] = true
		e.send(ProviderFailMsg{Provider: a.provider, Model: a.model, Reason: why})
		next, err := e.nextFallback(ctx, a, chain, tried, fmt.Errorf("%s: %s", a.provider, why))
		if err != nil {
			return nil, err
		}
		e.send(FallbackMsg{From: a.provider, To: next.provider + " · " + next.model})
		a = next
	}
	for {
		tried[a.key()] = true
		turn, err := e.attemptOnce(ctx, sys, a)
		if err == nil {
			if a != chain[0] {
				pin := a
				e.turnPin = &pin // stay on the fallback model for the rest of this turn
			}
			return turn, nil
		}
		if ctx.Err() != nil {
			return nil, context.Canceled
		}
		reason, sticky := Classify(a.provider, err)
		if sticky {
			e.disable(a.provider, reason)
		}
		lastErr := fmt.Errorf("%s: %s", a.provider, reason)
		e.send(ProviderFailMsg{Provider: a.provider, Model: a.model, Reason: reason})
		next, err := e.nextFallback(ctx, a, chain, tried, lastErr)
		if err != nil {
			return nil, err
		}
		e.send(FallbackMsg{From: a.provider, To: next.provider + " · " + next.model})
		a = next
	}
}

// attemptOnce sends one request to one model and records usage/cost.
func (e *Engine) attemptOnce(ctx context.Context, sys string, a attempt) (*llm.Turn, error) {
	mi := catalog.Lookup(a.provider, a.model)
	effort := ""
	if a.provider == e.cfg.Provider && a.model == e.cfg.Model {
		effort = e.cfg.Effort
	}
	fast := e.cfg.Fast && a.provider == e.cfg.Provider && a.model == e.cfg.Model
	req := llm.Request{Model: a.model, System: sys, History: e.history, Tools: tools.Defs, Effort: effort,
		Fast: fast, TTL1h: e.cfg.TTL1h(), CacheKey: e.sessionID}
	reqStart := time.Now()
	e.send(StreamStartMsg{Provider: a.provider, Model: a.model})
	turn, err := e.provider(a.provider).Stream(ctx, req, llm.Handlers{
		OnText: func(s string) {
			if !e.quiet {
				e.send(TextDeltaMsg(s))
			}
		},
		OnThinking: func(s string) {
			if !e.quiet {
				e.send(ThinkingDeltaMsg(s))
			}
		},
		OnToolStart: func(n string) {
			if !e.quiet {
				e.send(ToolPrepMsg(n))
			}
		},
	})
	if err != nil {
		return nil, err
	}
	cost := mi.Cost(turn.Usage.In, turn.Usage.Out, turn.Usage.CacheRead, turn.Usage.CacheWrite, req.Fast, req.TTL1h)
	if turn.Usage.CostUSD > 0 {
		cost = turn.Usage.CostUSD // the provider's own figure beats our estimate
	}
	unpriced := cost == 0 && !mi.Priced()
	e.lastContext = turn.Usage.In + turn.Usage.CacheRead + turn.Usage.CacheWrite + turn.Usage.Out
	e.lastWindow = mi.Context
	if turn.KeepAlive != nil {
		e.lastKA, e.lastReqAt, e.lastMI, e.lastFast = turn.KeepAlive, reqStart, mi, req.Fast
		e.lastPrefix = turn.Usage.In + turn.Usage.CacheRead + turn.Usage.CacheWrite
	}
	e.send(UsageMsg{Provider: a.provider, Model: a.model, Usage: turn.Usage, Cost: cost, Unpriced: unpriced,
		ContextUsed: int(turn.Usage.In + turn.Usage.CacheRead + turn.Usage.CacheWrite + turn.Usage.Out),
		ContextMax:  mi.Context})
	return turn, nil
}

func (e *Engine) execTool(ctx context.Context, tc llm.ToolCall) tools.Result {
	summary := tools.Summary(tc)
	e.send(ToolCallMsg{ID: tc.ID, Name: tc.Name, Summary: summary})

	need, kind := tools.NeedsPermission(tc.Name, e.mode())
	outside := false
	if a, err := tools.ParseArgs(tc); err == nil && fileTool(tc.Name) {
		path := a.Path
		if tools.Sensitive(path) {
			r := tools.Fail("%s is a credential location; werkz's file tools never open it", path)
			e.send(ToolResultMsg{ID: tc.ID, Name: tc.Name, Summary: "refused (credentials)", IsError: true})
			return r
		}
		// Reads, listings and searches with a path outside the working folders, and any write
		// outside them, always ask, in every mode. (Glob/Grep without a path stay inside.)
		if (path != "" || tc.Name == "Write" || tc.Name == "Edit") && !tools.InsideDirs(path, e.dirs()) {
			outside = true
			if kind != "plan" || !writeTool(tc.Name) {
				need, kind = true, "ask"
			}
			summary += "  ⚠ outside working folders"
		}
	}
	key, always := allowKey(tc)
	if outside {
		key, always = "", ""
	}
	if need && (key == "" || !e.allow[key]) {
		if kind == "plan" {
			r := tools.Result{Output: "Plan mode is on: only read-only tools may run. Describe the plan instead.", IsError: true}
			e.send(ToolResultMsg{ID: tc.ID, Name: tc.Name, Summary: "blocked (plan mode)", IsError: true})
			return r
		}
		reply := make(chan PermDecision, 1)
		e.send(PermReqMsg{Tool: tc.Name, Summary: summary, Detail: tools.Preview(tc), Always: always, Reply: reply})
		select {
		case d := <-reply:
			if d == PermAlways && key != "" {
				e.allow[key] = true
			}
			if d == PermNo {
				e.send(ToolResultMsg{ID: tc.ID, Name: tc.Name, Summary: "denied by user", IsError: true})
				return tools.Result{Output: "The user denied this action. Ask what they want instead.", IsError: true}
			}
		case <-ctx.Done():
			return tools.Result{Output: "Interrupted by user.", IsError: true}
		}
	}
	var res tools.Result
	if tc.Name == "Task" {
		res = e.runTask(ctx, tc)
	} else {
		res = tools.Run(ctx, tc, e.toolEnv())
	}
	e.trackCheck(tc, res)
	e.send(ToolResultMsg{ID: tc.ID, Name: tc.Name, Summary: res.Summary, Detail: res.Detail, IsError: res.IsError})
	return res
}

func fileTool(name string) bool {
	switch name {
	case "Read", "Write", "Edit", "LS", "Glob", "Grep":
		return true
	}
	return false
}

func writeTool(name string) bool { return name == "Write" || name == "Edit" }

// allowKey is what a "don't ask again" approval covers, and the label for that option.
// File tools are approved per tool. Bash is approved per command prefix (e.g. "go test"),
// and never for compound commands or risky programs, where it returns "".
func allowKey(tc llm.ToolCall) (key, label string) {
	if tc.Name != "Bash" {
		return tc.Name, "Yes, and don't ask again for " + tc.Name + " this session"
	}
	a, err := tools.ParseArgs(tc)
	if err != nil {
		return "", ""
	}
	p := commandPrefix(a.Command)
	if p == "" {
		return "", ""
	}
	return "Bash:" + p, "Yes, and don't ask again for `" + p + " …` commands this session"
}

// riskyCommands are never approved for the whole session.
var riskyCommands = map[string]bool{
	"rm": true, "sudo": true, "su": true, "doas": true, "dd": true, "mkfs": true, "curl": true, "wget": true,
	"ssh": true, "scp": true, "rsync": true, "chmod": true, "chown": true, "kill": true, "pkill": true,
	"killall": true, "shutdown": true, "reboot": true, "eval": true, "exec": true, "sh": true, "bash": true,
	"zsh": true, "python": true, "python3": true, "node": true, "perl": true, "ruby": true,
}

// commandPrefix returns the program and first sub-command (e.g. "go test", "npm run") of a
// simple command, or "" for compound/redirected commands and risky programs.
func commandPrefix(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" || strings.ContainsAny(cmd, ";&|`$<>(){}\n\\") {
		return ""
	}
	f := strings.Fields(cmd)
	if strings.Contains(f[0], "=") || riskyCommands[filepath.Base(f[0])] {
		return "" // env-var prefixes and risky programs always ask
	}
	if len(f) > 1 && !strings.HasPrefix(f[1], "-") && !strings.ContainsAny(f[1], "/.") {
		return f[0] + " " + f[1]
	}
	return f[0]
}

// autoCompact runs automatic compaction; repeat (if set) is re-added verbatim afterwards.
// It returns false when the turn must end (cancelled).
func (e *Engine) autoCompact(ctx context.Context, repeat string) bool {
	if err := e.compact(ctx, "", true); err != nil {
		if ctx.Err() != nil {
			e.finish(context.Canceled)
			return false
		}
		e.send(NoticeMsg{"warn", "Auto-compaction failed, continuing with the full context: " + err.Error()})
		e.autoCompactOff = true // don't retry every turn this session
		return true
	}
	if repeat != "" {
		e.appendHistory(llm.Msg{Role: llm.RoleUser, Text: "My current request, repeated word for word:\n\n" + repeat})
	}
	return true
}

// addMemory sends the project instruction files once per set of working folders (again after
// a workspace change or compaction), instead of re-reading and re-sending them every turn.
func (e *Engine) addMemory() {
	dirs := e.dirs()
	key := strings.Join(dirs, "\x00")
	if key == e.memDirs {
		return
	}
	e.memDirs = key
	if mem := MemoryContext(dirs); mem != "" {
		e.appendHistory(llm.Msg{Role: llm.RoleUser, Text: mem})
	}
}
