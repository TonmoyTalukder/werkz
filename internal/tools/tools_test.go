package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/llm"
)

func TestTrimOutput(t *testing.T) {
	out := "=== RUN   TestA\n--- PASS: TestA (0.00s)\n=== RUN   TestB\n    b_test.go:9: boom\n--- FAIL: TestB (0.00s)\nFAIL\nok  \tpkg/x\t0.1s\nFAIL\tpkg/y\t0.2s\n"
	got := trimOutput("go test ./...", out)
	for _, want := range []string{"b_test.go:9: boom", "--- FAIL: TestB", "FAIL\tpkg/y", "werkz trim"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	for _, gone := range []string{"=== RUN", "--- PASS", "ok  \tpkg/x"} {
		if strings.Contains(got, gone) {
			t.Fatalf("still has %q", gone)
		}
	}
	if trimOutput("go test -v ./...", out) != out || trimOutput("go test ./... | tee log", out) != out {
		t.Fatal("verbose/piped runs must not be trimmed")
	}
	if trimOutput("ls -la", out) != out {
		t.Fatal("unknown commands must not be trimmed")
	}
}

func TestStaleEditGuard(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.go")
	must(t, os.WriteFile(f, []byte("package a\nvar x = 1\n"), 0o644))
	env := Env{Dirs: []string{dir}, Reads: NewReadTracker()}
	edit := llm.ToolCall{Name: "Edit", Args: fmt.Sprintf(`{"path":%q,"old_string":"x = 1","new_string":"x = 2"}`, f)}
	if r := Run(context.Background(), edit, env); !r.IsError || !strings.Contains(r.Output, "before changing") {
		t.Fatalf("edit of unread file should fail: %s", r.Output)
	}
	Run(context.Background(), llm.ToolCall{Name: "Read", Args: fmt.Sprintf(`{"path":%q}`, f)}, env)
	if r := Run(context.Background(), edit, env); r.IsError {
		t.Fatalf("edit after read failed: %s", r.Output)
	}
	// external change after the read → must re-read
	time.Sleep(10 * time.Millisecond)
	must(t, os.WriteFile(f, []byte("package a\nvar x = 3\n"), 0o644))
	must(t, os.Chtimes(f, time.Now().Add(time.Second), time.Now().Add(time.Second)))
	edit2 := llm.ToolCall{Name: "Edit", Args: fmt.Sprintf(`{"path":%q,"old_string":"x = 3","new_string":"x = 4"}`, f)}
	if r := Run(context.Background(), edit2, env); !r.IsError || !strings.Contains(r.Output, "changed on disk") {
		t.Fatalf("stale edit should fail: %s", r.Output)
	}
	// new files can be written without reading
	if r := Run(context.Background(), llm.ToolCall{Name: "Write", Args: fmt.Sprintf(`{"path":%q,"content":"hi"}`, filepath.Join(dir, "new.txt"))}, env); r.IsError {
		t.Fatalf("new file write failed: %s", r.Output)
	}
}

func TestReadGuardsAndSpill(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.txt")
	must(t, os.WriteFile(big, []byte(strings.Repeat("0123456789abcdef\n", 20000)), 0o644)) // ~340 KB
	env := Env{Dirs: []string{dir}}
	if r := Run(context.Background(), llm.ToolCall{Name: "Read", Args: fmt.Sprintf(`{"path":%q}`, big)}, env); !r.IsError || !strings.Contains(r.Output, "offset/limit") {
		t.Fatalf("whole read of big file should be refused: %.100s", r.Output)
	}
	r := Run(context.Background(), llm.ToolCall{Name: "Read", Args: fmt.Sprintf(`{"path":%q,"offset":100,"limit":10}`, big)}, env)
	if r.IsError || !strings.Contains(r.Output, "   100\t") || !strings.Contains(r.Output, "offset=110") {
		t.Fatalf("ranged read: %s", r.Output)
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	r = Run(context.Background(), llm.ToolCall{Name: "Bash", Args: `{"command":"seq 1 20000"}`}, env)
	if !strings.Contains(r.Output, "full output saved to") || len(r.Output) > 12000 {
		t.Fatalf("spill missing or output too long (%d)", len(r.Output))
	}
	_, after, ok := strings.Cut(r.Output, "saved to ")
	path, _, ok2 := strings.Cut(after, " —")
	if !ok || !ok2 {
		t.Fatalf("no spill path in output: %q", r.Output)
	}
	if b, err := os.ReadFile(path); err != nil || !strings.Contains(string(b), "\n19999\n") {
		t.Fatalf("spill file: %v", err)
	}
}

func TestGrepModes(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		must(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d.go", i)), []byte("TODO one\nTODO two\nok\n"), 0o644))
	}
	env := Env{Dirs: []string{dir}}
	g := func(args string) string {
		return Run(context.Background(), llm.ToolCall{Name: "Grep", Args: fmt.Sprintf(`{"pattern":"TODO","path":%q%s}`, dir, args)}, env).Output
	}
	if out := g(""); strings.Count(out, "\n") != 2 || strings.Contains(out, "TODO one") {
		t.Fatalf("default should list files only: %q", out)
	}
	if out := g(`,"output_mode":"count"`); !strings.Contains(out, ".go:2") {
		t.Fatalf("count: %q", out)
	}
	if out := g(`,"output_mode":"content","head_limit":4`); strings.Count(out, "TODO") != 4 || !strings.Contains(out, "more not shown") {
		t.Fatalf("content+limit: %q", out)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// H4: a timeout or cancel kills the command's children; background jobs don't hang the
// turn; output is capped in memory.
func TestBashProcessControl(t *testing.T) {
	env := Env{Dirs: []string{t.TempDir()}}
	run := func(ctx context.Context, cmd string, timeout int) (Result, time.Duration) {
		start := time.Now()
		r := Run(ctx, llm.ToolCall{Name: "Bash", Args: fmt.Sprintf(`{"command":%q,"timeout_sec":%d}`, cmd, timeout)}, env)
		return r, time.Since(start)
	}
	if r, d := run(context.Background(), "sleep 8; echo never", 1); d > 4*time.Second || strings.Contains(r.Output, "never") || !strings.Contains(r.Output, "timed out") {
		t.Fatalf("timeout didn't kill the child: %v %q", d, r.Output)
	}
	if r, d := run(context.Background(), "(sleep 8) & echo started", 30); d > 5*time.Second || !strings.Contains(r.Output, "started") {
		t.Fatalf("background job blocked the turn: %v %q", d, r.Output)
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(500*time.Millisecond, cancel)
	if _, d := run(ctx, "sleep 8 | cat", 30); d > 4*time.Second {
		t.Fatalf("cancel (Esc) didn't stop the pipeline: %v", d)
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if r, _ := run(context.Background(), "yes | head -c 20000000", 30); !strings.Contains(r.Output, "output capped at 8 MB") {
		t.Fatalf("output not capped: %.200q", r.Output)
	}
}

// M1: a symlink inside a working folder that points outside it must not count as inside.
func TestInsideDirsResolvesSymlinks(t *testing.T) {
	proj, outside := t.TempDir(), t.TempDir()
	must(t, os.Symlink(outside, filepath.Join(proj, "docs")))
	must(t, os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o600))
	must(t, os.Symlink(filepath.Join(outside, "secret"), filepath.Join(proj, "link")))
	cases := map[string]bool{
		filepath.Join(proj, "main.go"):          true,  // new file inside
		filepath.Join(proj, "sub", "new.go"):    true,  // new file in a new folder inside
		filepath.Join(proj, "docs", "x.txt"):    false, // through a symlinked folder
		filepath.Join(proj, "link"):             false, // a symlinked file
		filepath.Join(proj, "..", "escape.txt"): false,
	}
	for p, want := range cases {
		if got := InsideDirs(p, []string{proj}); got != want {
			t.Errorf("InsideDirs(%s) = %v, want %v", p, got, want)
		}
	}
}

// M2/M3: credential locations are recognised (through symlinks too) and the home folder is
// never a project root.
func TestSensitivePathsAndHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	must(t, os.MkdirAll(filepath.Join(home, ".ssh"), 0o700))
	must(t, os.MkdirAll(filepath.Join(home, ".config", "werkz"), 0o700))
	proj := filepath.Join(home, "proj")
	must(t, os.MkdirAll(proj, 0o755))
	must(t, os.Symlink(filepath.Join(home, ".ssh"), filepath.Join(proj, "keys")))
	for p, want := range map[string]bool{
		"~/.ssh/id_ed25519": true,
		filepath.Join(home, ".config/werkz/config.json"): true,
		filepath.Join(proj, "keys", "id_rsa"):            true, // via symlink
		filepath.Join(proj, "main.go"):                   false,
	} {
		if got := Sensitive(p); got != want {
			t.Errorf("Sensitive(%s) = %v, want %v", p, got, want)
		}
	}
	if InsideDirs(filepath.Join(home, ".bashrc"), []string{home}) {
		t.Error("the home folder must not count as a project root")
	}
	if !InsideDirs(filepath.Join(proj, "a.go"), []string{proj}) {
		t.Error("a real project folder should still work")
	}
	r := Run(context.Background(), llm.ToolCall{Name: "Grep", Args: `{"pattern":"x"}`}, Env{Dirs: []string{home}})
	if !r.IsError || !strings.Contains(r.Output, "home folder") {
		t.Fatalf("a bare search in the home folder should be refused: %q", r.Output)
	}
}

// L2: a cancelled search stops instead of walking the whole tree.
func TestSearchStopsOnCancel(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 50; i++ {
		must(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%02d.txt", i)), []byte("needle\n"), 0o644))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tool := range []string{"Grep", "Glob"} {
		args := `{"pattern":"needle","output_mode":"count"}`
		if tool == "Glob" {
			args = `{"pattern":"*.txt"}`
		}
		r := Run(ctx, llm.ToolCall{Name: tool, Args: args}, Env{Dirs: []string{dir}})
		if !strings.Contains(r.Output, "interrupted") || strings.Count(r.Output, ".txt") > 1 {
			t.Errorf("%s ignored cancellation: %q", tool, r.Output)
		}
	}
}

// L3: "more lines follow" only when there are more lines; an unreadable long line is reported.
func TestReadContinuationHint(t *testing.T) {
	dir := t.TempDir()
	read := func(name string, content string, args string) string {
		p := filepath.Join(dir, name)
		must(t, os.WriteFile(p, []byte(content), 0o644))
		return Run(context.Background(), llm.ToolCall{Name: "Read", Args: fmt.Sprintf(`{"path":%q%s}`, p, args)}, Env{}).Output
	}
	exact := strings.Repeat("x\n", 2000) // exactly the default limit
	if out := read("exact.txt", exact, ""); strings.Contains(out, "more lines follow") {
		t.Fatal("a 2000-line file must not claim more lines follow")
	}
	if out := read("more.txt", exact+"last\n", ""); !strings.Contains(out, "offset=2001") {
		t.Fatal("a 2001-line file should point at offset=2001")
	}
	if out := read("range.txt", strings.Repeat("y\n", 30), `,"offset":10,"limit":5`); !strings.Contains(out, "offset=15") {
		t.Fatalf("ranged read hint wrong: %q", out)
	}
	huge := "a\n" + strings.Repeat("z", 9<<20) + "\nb\n" // a 9 MB line
	if out := read("huge.txt", huge, `,"offset":1,"limit":5`); !strings.Contains(out, "stopped at line 2") {
		t.Fatalf("over-long line should be reported: %.200q", out)
	}
}
