package tools

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func needPython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
}

func stopAll(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		for _, s := range DevServers() {
			_ = StopDevServer(s.ID)
		}
	})
}

func pidGone(pid int) bool {
	for range 50 {
		if syscall.Kill(pid, 0) != nil {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func TestDevServerPortReady(t *testing.T) {
	needPython(t)
	stopAll(t)
	port := freePort(t)
	// The call's ctx is cancelled right after start: the server must survive it.
	ctx, cancel := context.WithCancel(context.Background())
	s, err := StartDevServer(ctx, fmt.Sprintf("exec python3 -m http.server %d --bind 127.0.0.1", port), t.TempDir(), nil, port, 20*time.Second)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if s.Port != port || s.URL != fmt.Sprintf("http://localhost:%d/", port) || s.ID == 0 {
		t.Fatalf("bad server: %+v", s)
	}
	time.Sleep(300 * time.Millisecond)
	if !s.Running() || s.ExitInfo() != "" {
		t.Fatalf("should still run: %q", s.ExitInfo())
	}
	if got := DevServers(); len(got) != 1 || got[0] != s {
		t.Fatalf("DevServers = %v", got)
	}
	if _, err := StartDevServer(context.Background(), "sleep 5", "", nil, port, time.Second); err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("want port-in-use error, got %v", err)
	}
	if err := StopDevServer(s.ID); err != nil {
		t.Fatal(err)
	}
	if s.Running() || !strings.HasPrefix(s.ExitInfo(), "terminated by") {
		t.Fatalf("after stop: running=%v info=%q", s.Running(), s.ExitInfo())
	}
	if len(DevServers()) != 0 {
		t.Fatal("still listed")
	}
	if portOpen(port) {
		t.Fatal("port still open")
	}
}

func TestDevServerURLDetection(t *testing.T) {
	stopAll(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	cmd := fmt.Sprintf(`printf '\033[32m  Local:\033[0m   \033[36mhttp://localhost:\033[1m%d\033[22m/\033[0m\n'; sleep 30`, port)
	s, err := StartDevServer(context.Background(), cmd, "", []string{"FOO=bar"}, 0, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if s.Port != port || s.URL != fmt.Sprintf("http://localhost:%d/", port) {
		t.Fatalf("port/url = %d %q", s.Port, s.URL)
	}
	logs := s.Logs(0)
	if strings.Contains(logs, "\x1b") || !strings.Contains(logs, "Local:") {
		t.Fatalf("logs not stripped: %q", logs)
	}
	_ = StopDevServer(s.ID)
}

func TestDevServerEnvAndDir(t *testing.T) {
	stopAll(t)
	dir := t.TempDir()
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	s, err := StartDevServer(context.Background(), fmt.Sprintf(`echo "$WERKZ_X $(pwd)"; echo http://127.0.0.1:%d/app; sleep 30`, port), dir, []string{"WERKZ_X=hello"}, 0, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = StopDevServer(s.ID) }()
	real, _ := filepath.EvalSymlinks(dir)
	if !strings.Contains(s.Logs(5), "hello "+real) && !strings.Contains(s.Logs(5), "hello "+dir) {
		t.Fatalf("logs: %q", s.Logs(5))
	}
	if s.URL != fmt.Sprintf("http://127.0.0.1:%d/app", port) {
		t.Fatalf("url %q", s.URL)
	}
}

func TestDevServerExitsEarly(t *testing.T) {
	stopAll(t)
	_, err := StartDevServer(context.Background(), `echo boom; exit 3`, "", nil, freePort(t), 10*time.Second)
	if err == nil || !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "code 3") {
		t.Fatalf("got %v", err)
	}
	if len(DevServers()) != 0 {
		t.Fatal("failed server still listed")
	}
}

func TestDevServerTimeout(t *testing.T) {
	stopAll(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	start := time.Now()
	_, err := StartDevServer(context.Background(), `echo $$ > `+pidFile+`; echo waiting; sleep 300`, "", nil, freePort(t), 600*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "not ready") || !strings.Contains(err.Error(), "waiting") {
		t.Fatalf("got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("took too long")
	}
	pid := readPid(t, pidFile)
	if !pidGone(pid) {
		t.Fatalf("process %d survived timeout", pid)
	}
}

func TestDevServerCtxCancel(t *testing.T) {
	stopAll(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(400*time.Millisecond, cancel)
	_, err := StartDevServer(ctx, `echo $$ > `+pidFile+`; sleep 300`, "", nil, freePort(t), 30*time.Second)
	if err != context.Canceled {
		t.Fatalf("got %v", err)
	}
	if pid := readPid(t, pidFile); !pidGone(pid) {
		t.Fatalf("process %d survived cancel", pid)
	}
	if len(DevServers()) != 0 {
		t.Fatal("still listed")
	}
}

func readPid(t *testing.T, path string) int {
	t.Helper()
	var b []byte
	for range 50 {
		var err error
		if b, err = os.ReadFile(path); err == nil && strings.Contains(string(b), "\n") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatalf("pid file %q: %v", b, err)
	}
	return pid
}

func TestDevServerStopKillsGroup(t *testing.T) {
	needPython(t)
	stopAll(t)
	port := freePort(t)
	pidFile := filepath.Join(t.TempDir(), "child")
	s, err := StartDevServer(context.Background(),
		fmt.Sprintf("sleep 300 & echo $! > %s; python3 -m http.server %d --bind 127.0.0.1", pidFile, port), "", nil, port, 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	child := readPid(t, pidFile)
	if syscall.Kill(child, 0) != nil {
		t.Fatal("child not running")
	}
	if err := StopDevServer(s.ID); err != nil {
		t.Fatal(err)
	}
	if !pidGone(child) {
		t.Fatalf("child %d survived StopDevServer", child)
	}
	if err := StopDevServer(s.ID); err == nil {
		t.Fatal("second stop should report unknown id")
	}
}

func TestDevServerKillAll(t *testing.T) {
	stopAll(t)
	var servers []*DevServer
	for range 2 {
		l, _ := net.Listen("tcp", "127.0.0.1:0")
		defer l.Close()
		port := l.Addr().(*net.TCPAddr).Port
		s, err := StartDevServer(context.Background(), fmt.Sprintf("echo http://localhost:%d; sleep 300", port), "", nil, 0, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		servers = append(servers, s)
	}
	KillAll()
	for _, s := range servers {
		select {
		case <-s.done:
		case <-time.After(3 * time.Second):
			t.Fatalf("server %d survived KillAll", s.ID)
		}
		if s.Running() || s.ExitInfo() == "" {
			t.Fatalf("server %d: running=%v", s.ID, s.Running())
		}
	}
	if len(DevServers()) != 0 {
		t.Fatal("still listed")
	}
}

func TestDevServerMax(t *testing.T) {
	stopAll(t)
	for i := range maxDevServers {
		l, _ := net.Listen("tcp", "127.0.0.1:0")
		defer l.Close()
		port := l.Addr().(*net.TCPAddr).Port
		if _, err := StartDevServer(context.Background(), fmt.Sprintf("echo http://localhost:%d; sleep 300", port), "", nil, 0, 10*time.Second); err != nil {
			t.Fatalf("server %d: %v", i, err)
		}
	}
	if _, err := StartDevServer(context.Background(), "sleep 300", "", nil, freePort(t), time.Second); err == nil || !strings.Contains(err.Error(), "max 4") {
		t.Fatalf("got %v", err)
	}
}

func TestDevServerLogs(t *testing.T) {
	s := &DevServer{}
	_, _ = s.Write([]byte("\x1b[31mred\x1b[0m\nline2\r\n" + strings.Repeat("x", 500) + "\npart"))
	got := strings.Split(s.Logs(10), "\n")
	if len(got) != 4 || got[0] != "red" || got[1] != "line2" || got[3] != "part" || len([]rune(got[2])) != devLogLineMax+1 {
		t.Fatalf("logs = %q", got)
	}
	_, _ = s.Write([]byte("\n"))
	for i := range 3000 {
		_, _ = fmt.Fprintf(s, "n%d\n", i)
	}
	_, _ = s.Write([]byte("part"))
	all := strings.Split(s.Logs(10000), "\n")
	if len(all) != devLogMax || all[len(all)-2] != "n2999" || all[len(all)-1] != "part" {
		t.Fatalf("len=%d tail=%q", len(all), all[len(all)-2:])
	}
	s.mu.Lock()
	n := len(s.lines)
	s.mu.Unlock()
	if n > 2*devLogLines {
		t.Fatalf("ring grew to %d", n)
	}
}
