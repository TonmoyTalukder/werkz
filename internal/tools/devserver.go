package tools

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxDevServers      = 4
	devLogLines        = 2000
	devLogDefault      = 80
	devLogMax          = 400
	devLogLineMax      = 400
	devErrLogLines     = 40
	devDefaultTimeout  = 90 * time.Second
	devPollInterval    = 100 * time.Millisecond
	devStopWait        = 3 * time.Second
	devMaxPartial      = 64 << 10
	devKeepExitedCount = 8
)

var (
	ansiRe   = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	devURLRe = regexp.MustCompile(`https?://(?:localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\]):(\d+)[^\s]*`)
)

// DevServer is a dev server started in the background by StartDevServer.
type DevServer struct {
	ID      int
	Command string
	Dir     string
	Port    int
	URL     string
	Started time.Time

	cmd  *exec.Cmd
	done chan struct{} // closed once the process has been reaped

	mu       sync.Mutex
	lines    []string // completed lines, ANSI stripped
	partial  string   // unterminated last line
	ready    bool     // Port/URL are final; visible to DevServers/FindDevServer
	exited   bool
	exitInfo string
}

var (
	devMu     sync.Mutex
	devNextID = 1
	devAll    = map[int]*DevServer{} // running servers plus a few recently exited ones
)

// Write implements io.Writer for the process's merged stdout/stderr.
func (s *DevServer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.partial += string(p)
	for {
		i := strings.IndexByte(s.partial, '\n')
		if i < 0 {
			break
		}
		s.addLine(s.partial[:i])
		s.partial = s.partial[i+1:]
	}
	if len(s.partial) > devMaxPartial {
		s.addLine(s.partial)
		s.partial = ""
	}
	return len(p), nil
}

func (s *DevServer) addLine(l string) {
	l = strings.TrimRight(ansiRe.ReplaceAllString(l, ""), "\r")
	if i := strings.LastIndexByte(l, '\r'); i >= 0 { // progress bars rewrite the line
		l = l[i+1:]
	}
	s.lines = append(s.lines, l)
	if len(s.lines) >= 2*devLogLines {
		s.lines = append([]string(nil), s.lines[len(s.lines)-devLogLines:]...)
	}
}

// Logs returns the last n lines of output (default 80, max 400), ANSI stripped and long lines cut.
func (s *DevServer) Logs(n int) string {
	if n <= 0 {
		n = devLogDefault
	}
	n = min(n, devLogMax)
	s.mu.Lock()
	all := s.lines
	if len(all) > devLogLines {
		all = all[len(all)-devLogLines:]
	}
	out := append([]string(nil), all...)
	if s.partial != "" {
		out = append(out, strings.TrimRight(ansiRe.ReplaceAllString(s.partial, ""), "\r"))
	}
	s.mu.Unlock()
	if len(out) > n {
		out = out[len(out)-n:]
	}
	for i, l := range out {
		if r := []rune(l); len(r) > devLogLineMax {
			out[i] = string(r[:devLogLineMax]) + "…"
		}
	}
	return strings.Join(out, "\n")
}

// Running reports whether the process is still alive.
func (s *DevServer) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.exited
}

// ExitInfo is "" while running, else how the process ended.
func (s *DevServer) ExitInfo() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exitInfo
}

// printedURL returns the port of the first local URL in the output, and the URL as printed.
func (s *DevServer) printedURL() (int, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	check := func(l string) (int, string) {
		m := devURLRe.FindStringSubmatch(l)
		if m == nil {
			return 0, ""
		}
		p, err := strconv.Atoi(m[1])
		if err != nil || p <= 0 || p > 65535 {
			return 0, ""
		}
		return p, strings.Replace(m[0], "0.0.0.0", "localhost", 1)
	}
	for _, l := range s.lines {
		if p, u := check(l); p > 0 {
			return p, u
		}
	}
	return check(ansiRe.ReplaceAllString(s.partial, ""))
}

// portOpen reports whether something accepts TCP connections on loopback port.
func portOpen(port int) bool {
	for _, host := range []string{"127.0.0.1", "::1"} {
		c, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 300*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return true
		}
	}
	return false
}

// reap waits for the process, records how it ended and releases its tracking.
func (s *DevServer) reap() {
	err := s.cmd.Wait()
	_ = err // exit status is read from ProcessState; ErrWaitDelay just means a stray child held the pipe
	// Anything the shell left behind in the group goes too: a dev server that "exited" must not leak.
	_ = killGroup(s.cmd)
	untrack(s.cmd)
	info := "exited"
	if ps := s.cmd.ProcessState; ps != nil {
		if c := ps.ExitCode(); c >= 0 {
			info = fmt.Sprintf("exited with code %d", c)
		} else {
			info = "terminated by " + strings.TrimPrefix(ps.String(), "signal: ")
		}
	}
	s.mu.Lock()
	s.exited, s.exitInfo = true, info
	s.mu.Unlock()
	close(s.done)
}

func (s *DevServer) kill() {
	_ = killGroup(s.cmd)
	select {
	case <-s.done:
	case <-time.After(devStopWait):
	}
}

func (s *DevServer) fail(format string, args ...any) error {
	return fmt.Errorf("%s\nlast output:\n%s", fmt.Sprintf(format, args...), s.Logs(devErrLogLines))
}

func forget(s *DevServer) {
	devMu.Lock()
	delete(devAll, s.ID)
	devMu.Unlock()
}

// StartDevServer runs command with $SHELL -c in dir (env appended to os.Environ()) in its own process group
// and waits until it serves. port > 0 waits for that port; port == 0 waits for a printed local URL whose
// port accepts connections. The server keeps running after the call returns (until StopDevServer/KillAll).
func StartDevServer(ctx context.Context, command, dir string, env []string, port int, timeout time.Duration) (*DevServer, error) {
	if strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("devserver: empty command")
	}
	if port < 0 || port > 65535 {
		return nil, fmt.Errorf("devserver: invalid port %d", port)
	}
	if timeout <= 0 {
		timeout = devDefaultTimeout
	}
	if port > 0 && portOpen(port) {
		return nil, fmt.Errorf("port %d is already in use: something is already listening there (it may be the user's own dev server); "+
			"use that instead, or pick another port", port)
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell, "-c", command) // not CommandContext: the server outlives this call's ctx
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	setProcessGroup(cmd)
	cmd.Cancel = nil                // only valid with CommandContext; we kill the group ourselves
	cmd.WaitDelay = 2 * time.Second // a stray child holding the pipe must not block reaping
	s := &DevServer{Command: command, Dir: dir, Port: port, Started: time.Now(), cmd: cmd, done: make(chan struct{})}
	cmd.Stdout, cmd.Stderr = s, s

	devMu.Lock()
	n := 0
	for _, o := range devAll {
		if o.Running() {
			n++
		}
	}
	if n >= maxDevServers {
		devMu.Unlock()
		return nil, fmt.Errorf("already running %d dev servers (max %d): stop one first", n, maxDevServers)
	}
	if err := cmd.Start(); err != nil {
		devMu.Unlock()
		return nil, fmt.Errorf("start dev server: %w", err)
	}
	track(cmd)
	s.ID = devNextID
	devNextID++
	devAll[s.ID] = s
	devMu.Unlock()
	go s.reap()
	go pruneExited()

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(devPollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			s.kill()
			forget(s)
			return nil, ctx.Err()
		case <-s.done:
			forget(s)
			return nil, s.fail("dev server %s before it was ready (%s)", s.ExitInfo(), command)
		case <-deadline.C:
			s.kill()
			forget(s)
			return nil, s.fail("dev server not ready after %s (%s); it was stopped", timeout.Round(time.Second), command)
		case <-tick.C:
		}
		p, u := port, ""
		if p == 0 {
			if p, u = s.printedURL(); p == 0 {
				continue
			}
		}
		if !portOpen(p) {
			continue
		}
		if u == "" {
			u = fmt.Sprintf("http://localhost:%d/", p)
		}
		s.mu.Lock()
		s.Port, s.URL, s.ready = p, u, true
		s.mu.Unlock()
		return s, nil
	}
}

// pruneExited keeps only the most recent exited servers in the registry.
func pruneExited() {
	devMu.Lock()
	defer devMu.Unlock()
	var dead []int
	for id, s := range devAll {
		if !s.Running() {
			dead = append(dead, id)
		}
	}
	sort.Ints(dead)
	for len(dead) > devKeepExitedCount {
		delete(devAll, dead[0])
		dead = dead[1:]
	}
}

// DevServers lists the running dev servers, by ID.
func DevServers() []*DevServer {
	devMu.Lock()
	defer devMu.Unlock()
	var out []*DevServer
	for _, s := range devAll {
		if s.Running() && s.isReady() {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// FindDevServer returns a dev server by ID, running or recently exited (so its logs can still be read).
func FindDevServer(id int) *DevServer {
	devMu.Lock()
	defer devMu.Unlock()
	if s := devAll[id]; s != nil && s.isReady() {
		return s
	}
	return nil
}

func (s *DevServer) isReady() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

// StopDevServer kills the server's process group and waits up to 3s for it to be reaped.
func StopDevServer(id int) error {
	devMu.Lock()
	s := devAll[id]
	devMu.Unlock()
	if s == nil {
		return fmt.Errorf("no dev server with id %d", id)
	}
	_ = killGroup(s.cmd)
	select {
	case <-s.done:
	case <-time.After(devStopWait):
		return fmt.Errorf("dev server %d did not exit within %s", id, devStopWait)
	}
	forget(s)
	return nil
}
