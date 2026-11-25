package tools

import (
	"os/exec"
	"sync"
)

// maxCapture caps how much command output is kept in memory.
const maxCapture = 8 << 20

// cappedBuffer keeps the first limit bytes written to it and counts the rest.
type cappedBuffer struct {
	mu      sync.Mutex
	b       []byte
	limit   int
	dropped int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	room := c.limit - len(c.b)
	switch {
	case room <= 0:
		c.dropped += len(p)
	case len(p) > room:
		c.b = append(c.b, p[:room]...)
		c.dropped += len(p) - room
	default:
		c.b = append(c.b, p...)
	}
	return len(p), nil // never fail the command because output was capped
}

func (c *cappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.b)
}

// Commands started by Bash, so their process groups can be killed when werkz exits.
var (
	runningMu sync.Mutex
	running   = map[*exec.Cmd]struct{}{}
)

func track(cmd *exec.Cmd) {
	runningMu.Lock()
	running[cmd] = struct{}{}
	runningMu.Unlock()
}

func untrack(cmd *exec.Cmd) {
	runningMu.Lock()
	delete(running, cmd)
	runningMu.Unlock()
}

// KillAll kills the process groups of commands that are still running. Call it on exit.
func KillAll() {
	runningMu.Lock()
	defer runningMu.Unlock()
	for cmd := range running {
		_ = killGroup(cmd)
	}
}
