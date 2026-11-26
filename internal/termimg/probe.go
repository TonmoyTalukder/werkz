package termimg

import (
	"bytes"
	"context"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

const probeTimeout = 400 * time.Millisecond

// probeQuery asks for Kitty graphics support, the cell size in pixels
// (CSI 16 t) and, last, the primary device attributes (DA1). Terminals answer
// in order, so the DA1 reply marks the end of the conversation.
const probeQuery = "\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\" + "\x1b[16t" + "\x1b[c"

type probeResult struct {
	kitty        bool
	sixel        bool
	gotDA1       bool
	cellW, cellH int
}

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
	if caps.InTmux || strings.TrimSpace(os.Getenv("WERKZ_IMAGES")) != "" {
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

// mergeProbe folds a probe result into caps; explicit/env answers win.
func mergeProbe(caps *Caps, res probeResult) {
	if caps.CellW == 0 || caps.CellH == 0 {
		caps.CellW, caps.CellH = res.cellW, res.cellH
	}
	if caps.Proto != None {
		return
	}
	switch {
	case res.kitty:
		caps.Proto = Kitty // placeholders only when env identified kitty/ghostty
	case res.sixel:
		caps.Proto = Sixel
	}
}

// probeWith writes the query to out and parses replies from in until the DA1
// answer or the timeout.
func probeWith(in io.Reader, out io.Writer, timeout time.Duration) probeResult {
	var res probeResult
	if _, err := io.WriteString(out, probeQuery); err != nil {
		return res
	}
	data := make(chan []byte)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		buf := make([]byte, 512)
		for {
			n, err := in.Read(buf)
			if n > 0 {
				select {
				case data <- append([]byte(nil), buf[:n]...):
				case <-stop:
					return
				}
			}
			if err != nil {
				select {
				case data <- nil:
				case <-stop:
				}
				return
			}
		}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var acc []byte
	for {
		select {
		case b := <-data:
			if b == nil {
				return res
			}
			acc = append(acc, b...)
			if parseReplies(acc, &res) {
				return res
			}
		case <-timer.C:
			return res
		}
	}
}

// parseReplies scans accumulated reply bytes; it reports true once DA1 seen.
func parseReplies(acc []byte, res *probeResult) bool {
	*res = probeResult{}
	for i := 0; i < len(acc); i++ {
		if acc[i] != 0x1b || i+1 >= len(acc) {
			continue
		}
		switch acc[i+1] {
		case '_': // APC: kitty graphics reply, ends with ST
			end := bytes.Index(acc[i:], []byte(st))
			if end < 0 {
				return false
			}
			body := string(acc[i+2 : i+end])
			if strings.HasPrefix(body, "G") && strings.Contains(body, "i=31") &&
				strings.Contains(body, ";OK") {
				res.kitty = true
			}
			i += end + 1
		case '[': // CSI
			j := i + 2
			for j < len(acc) && (acc[j] < 0x40 || acc[j] > 0x7e) {
				j++
			}
			if j >= len(acc) {
				return false
			}
			params, final := string(acc[i+2:j]), acc[j]
			switch {
			case final == 'c' && strings.HasPrefix(params, "?"):
				for _, a := range strings.Split(params[1:], ";") {
					if a == "4" {
						res.sixel = true
					}
				}
				res.gotDA1 = true
			case final == 't' && strings.HasPrefix(params, "6;"):
				f := strings.Split(params, ";")
				if len(f) == 3 {
					h, _ := strconv.Atoi(f[1])
					w, _ := strconv.Atoi(f[2])
					res.cellH, res.cellW = h, w
				}
			}
			i = j
		}
	}
	return res.gotDA1
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
