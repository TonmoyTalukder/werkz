//go:build unix

package termimg

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// Probe asks the terminal what it supports. It must run before the
// full-screen UI starts. FromEnv's explicit answers win; it never probes
// inside tmux or when stdin/stdout are not terminals. The terminal mode is
// always restored.
func Probe(ctx context.Context) Caps {
	caps := FromEnv(os.Getenv)
	in, out := int(os.Stdin.Fd()), int(os.Stdout.Fd())
	if !term.IsTerminal(in) || !term.IsTerminal(out) {
		return caps
	}
	if ws, err := unix.IoctlGetWinsize(out, unix.TIOCGWINSZ); err == nil &&
		ws.Col > 0 && ws.Row > 0 && ws.Xpixel > 0 && ws.Ypixel > 0 {
		caps.CellW, caps.CellH = int(ws.Xpixel)/int(ws.Col), int(ws.Ypixel)/int(ws.Row)
	}
	if caps.InTmux {
		return InTmuxCaps(caps, os.Getenv, tmuxPassthrough(ctx))
	}
	if strings.TrimSpace(os.Getenv("WERKZ_IMAGES")) != "" {
		return caps
	}
	old, err := term.MakeRaw(in)
	if err != nil {
		return caps
	}
	defer func() { _ = term.Restore(in, old) }()

	done := make(chan struct{})
	defer close(done)
	res := probeWith(&pollReader{fd: in, done: done, ctx: ctx}, os.Stdout, probeTimeout)
	mergeProbe(&caps, res)
	return caps
}

// pollReader reads a tty fd in short poll slices so the reading goroutine can
// exit when done closes (instead of swallowing later keystrokes).
type pollReader struct {
	fd   int
	done <-chan struct{}
	ctx  context.Context
}

func (p *pollReader) Read(b []byte) (int, error) {
	for {
		select {
		case <-p.done:
			return 0, io.EOF
		case <-p.ctx.Done():
			return 0, p.ctx.Err()
		default:
		}
		fds := []unix.PollFd{{Fd: int32(p.fd), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, 50)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return 0, err
		}
		if n > 0 {
			return unix.Read(p.fd, b)
		}
	}
}

// tmuxPassthrough is the pane's allow-passthrough option ("" if tmux can't be asked).
func tmuxPassthrough(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "show-options", "-Apv", "allow-passthrough").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
