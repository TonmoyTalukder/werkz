package clipboard

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func fake(env map[string]string, have ...string) (Env, *[]string, *bytes.Buffer) {
	var ran []string
	var term bytes.Buffer
	return Env{
		Getenv: func(k string) string { return env[k] },
		LookPath: func(n string) (string, error) {
			for _, h := range have {
				if h == n {
					return "/usr/bin/" + n, nil
				}
			}
			return "", errors.New("not found")
		},
		Run: func(_ context.Context, name string, args []string, stdin string) error {
			ran = append(ran, name)
			if name == "tmux" && env["TMUX_FAIL"] != "" {
				return errors.New("no")
			}
			return nil
		},
		Terminal: &term, GOOS: "linux",
	}, &ran, &term
}

func TestCopyChoosesTheRightWay(t *testing.T) {
	e, ran, term := fake(map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, "wl-copy", "xclip")
	if r, err := CopyWith(e, "hi"); err != nil || r.Via != "wl-copy" || !r.Confirmed || term.Len() != 0 {
		t.Fatalf("wayland: %+v %v (ran %v)", r, err, *ran)
	}
	e, _, term = fake(map[string]string{"SSH_TTY": "/dev/pts/1"}, "xclip")
	if r, _ := CopyWith(e, "hi"); r.Via != "OSC 52" || r.Confirmed || !strings.Contains(term.String(), "\x1b]52;c;aGk=\x07") {
		t.Fatalf("ssh: %+v %q", r, term.String())
	}
	e, ran, _ = fake(map[string]string{"TMUX": "/tmp/tmux", "SSH_TTY": "x"})
	if r, _ := CopyWith(e, "hi"); r.Via != "tmux" || (*ran)[0] != "tmux" {
		t.Fatalf("tmux: %+v", r)
	}
	e, _, term = fake(map[string]string{}) // no desktop, no tools
	if r, _ := CopyWith(e, "hi"); r.Via != "OSC 52" || term.Len() == 0 {
		t.Fatalf("fallback: %+v", r)
	}
	if _, err := CopyWith(e, strings.Repeat("x", MaxOSC52+1)); err == nil {
		t.Fatal("oversized OSC 52 sent")
	}
	e, _, term = fake(map[string]string{"STY": "1.pts"})
	_, _ = CopyWith(e, "hi")
	if !strings.HasPrefix(term.String(), "\x1bP") {
		t.Fatalf("screen not wrapped: %q", term.String())
	}
}

func TestX11WhenNoTool(t *testing.T) {
	var got string
	x11 := func(_ context.Context, s string) error { got = s; return nil }
	e, _, term := fake(map[string]string{"DISPLAY": ":0", "WAYLAND_DISPLAY": "wayland-0"}) // no wl-copy/xclip installed
	e.X11 = x11
	if r, err := CopyWith(e, "hi"); err != nil || r.Via != "the X11 clipboard" || !r.Confirmed || got != "hi" || term.Len() != 0 {
		t.Fatalf("x11: %+v %v (osc52 %q)", r, err, term.String())
	}
	got = ""
	e, _, _ = fake(map[string]string{"DISPLAY": ":0"}, "xclip") // a tool wins
	e.X11 = x11
	if r, _ := CopyWith(e, "hi"); r.Via != "xclip" || got != "" {
		t.Fatalf("tool not preferred: %+v", r)
	}
	e, _, term = fake(map[string]string{"DISPLAY": ":0"})
	e.X11 = func(context.Context, string) error { return errors.New("no X") }
	if r, _ := CopyWith(e, "hi"); r.Via != "OSC 52" || term.Len() == 0 {
		t.Fatalf("failed X11 should fall back to OSC 52: %+v", r)
	}
}
