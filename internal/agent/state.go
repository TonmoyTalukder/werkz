package agent

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/TonmoyTalukder/werkz/internal/llm"
	"github.com/TonmoyTalukder/werkz/internal/tools"
)

// The UI and the engine run in different goroutines. The engine owns the conversation and
// writes it only from its own goroutine; everything the UI may read or change while a turn
// is running goes through these methods, guarded by stateMu (or atomics).

type sharedState struct {
	stateMu sync.Mutex
	modeV   atomic.Value // permission mode; may change mid-turn (shift+tab)
}

// Dirs returns the working folders (main first), or the current directory when none are set.
func (e *Engine) Dirs() []string {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	if len(e.dirList) > 0 {
		return append([]string(nil), e.dirList...)
	}
	wd, _ := os.Getwd()
	return []string{wd}
}

func (e *Engine) dirs() []string { return e.Dirs() }

// SetWorkspace sets the working folders, extra env vars and workspace name ("" = none).
func (e *Engine) SetWorkspace(dirs, env []string, name string) {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	e.dirList = append([]string(nil), dirs...)
	e.env = append([]string(nil), env...)
	e.wsName = name
}

// AddDir appends a working folder and returns the new list.
func (e *Engine) AddDir(dir string) []string {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	if len(e.dirList) == 0 {
		wd, _ := os.Getwd()
		e.dirList = []string{wd}
	}
	e.dirList = append(e.dirList, dir)
	return append([]string(nil), e.dirList...)
}

// Workspace reports the open workspace name ("" = none), its folder count and env var keys.
func (e *Engine) Workspace() (name string, folders int, env []string) {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	return e.wsName, len(e.dirList), append([]string(nil), e.env...)
}

func (e *Engine) workspaceEnv() ([]string, string) {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	return append([]string(nil), e.env...), e.wsName
}

// HistoryLen is the number of messages in the conversation.
func (e *Engine) HistoryLen() int {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	return len(e.history)
}

func (e *Engine) appendHistory(m ...llm.Msg) {
	e.stateMu.Lock()
	e.history = append(e.history, m...)
	e.stateMu.Unlock()
}

func (e *Engine) setHistory(h []llm.Msg) {
	e.stateMu.Lock()
	e.history = h
	e.stateMu.Unlock()
}

// Disabled returns why a provider is unusable this session ("" = usable).
func (e *Engine) Disabled(provider string) string {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	return e.disabled[provider]
}

func (e *Engine) disable(provider, reason string) {
	e.stateMu.Lock()
	e.disabled[provider] = reason
	e.stateMu.Unlock()
}

// SetMode changes the permission mode; it takes effect immediately, even mid-turn.
func (e *Engine) SetMode(mode string) { e.modeV.Store(mode) }

func (e *Engine) mode() string {
	if v, ok := e.modeV.Load().(string); ok {
		return v
	}
	return e.cfg.Mode
}

// Enable makes a provider usable again after it was disabled this session (e.g. once the user
// has fixed its key or topped up credits).
func (e *Engine) Enable(provider string) {
	e.stateMu.Lock()
	delete(e.disabled, provider)
	e.stateMu.Unlock()
}

// History returns a copy of the conversation (for saving it).
func (e *Engine) History() []llm.Msg {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	return append([]llm.Msg(nil), e.history...)
}

// Restore replaces the conversation with a saved one, to continue it. Call it between turns,
// after the saved conversation's working folders are set: their project instructions are
// already part of it. Files must be read again before they are edited.
func (e *Engine) Restore(h []llm.Msg) {
	e.StopKeepAlive()
	e.setHistory(append([]llm.Msg(nil), h...))
	e.envSent, e.lastKA = "", nil
	e.memDirs = strings.Join(e.dirs(), "\x00")
	e.reads = tools.NewReadTracker()
	e.attached = nil
	e.lastContext = 0
}
