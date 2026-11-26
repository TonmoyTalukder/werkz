// Package clipboard copies text to the system clipboard from a terminal app: with the
// platform's clipboard tool when there is one (pbcopy, wl-copy, xclip, xsel, PowerShell on
// WSL), into tmux's buffer inside tmux, and with the OSC 52 terminal sequence over SSH or when
// no tool works. It never reads the clipboard.
package clipboard

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// MaxOSC52 is the most text sent with OSC 52; many terminals drop longer sequences.
const MaxOSC52 = 100_000

// Result says how the text was copied.
type Result struct {
	Via       string // e.g. "wl-copy", "tmux", "OSC 52"
	Confirmed bool   // false for OSC 52: terminals never confirm it
}

// Env is what Copy looks at; tests replace it.
type Env struct {
	Getenv   func(string) string
	LookPath func(string) (string, error)
	Run      func(ctx context.Context, name string, args []string, stdin string) error
	Terminal io.Writer // where OSC 52 goes (the terminal)
	GOOS     string
	X11      func(ctx context.Context, text string) error // werkz's own X11 clipboard (nil = none)
}

func defaultEnv() Env {
	return Env{Getenv: os.Getenv, LookPath: exec.LookPath, Run: run, Terminal: os.Stdout, GOOS: runtime.GOOS, X11: x11Copy}
}

// Copy puts text on the clipboard.
func Copy(text string) (Result, error) { return CopyWith(defaultEnv(), text) }

type tool struct {
	name string
	args []string
}

// CopyWith is Copy with a given environment.
func CopyWith(e Env, text string) (Result, error) {
	ssh := e.Getenv("SSH_TTY") != "" || e.Getenv("SSH_CONNECTION") != "" || e.Getenv("SSH_CLIENT") != ""
	desktop := e.GOOS == "darwin" || e.GOOS == "windows" || e.Getenv("WAYLAND_DISPLAY") != "" ||
		e.Getenv("DISPLAY") != "" || e.Getenv("WSL_DISTRO_NAME") != ""

	var res Result
	tmuxOK := false
	if e.Getenv("TMUX") != "" {
		// tmux 3.2+ passes -w on to the outer terminal's clipboard (when it allows it)
		if e.Run(context.Background(), "tmux", []string{"load-buffer", "-w", "-"}, text) == nil {
			tmuxOK, res = true, Result{Via: "tmux", Confirmed: true}
		}
	}
	if !ssh || desktop {
		for _, t := range tools(e) {
			if _, err := e.LookPath(t.name); err != nil {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			err := e.Run(ctx, t.name, t.args, text)
			cancel()
			if err == nil {
				return Result{Via: t.name, Confirmed: true}, nil
			}
		}
	}
	// No clipboard tool: on a desktop with X11 (including GNOME and KDE on Wayland, through
	// XWayland), werkz serves the clipboard itself.
	if e.X11 != nil && e.Getenv("DISPLAY") != "" && e.GOOS != "darwin" && e.GOOS != "windows" {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		err := e.X11(ctx, text)
		cancel()
		if err == nil {
			return Result{Via: "the X11 clipboard", Confirmed: true}, nil
		}
	}
	if tmuxOK {
		return res, nil
	}
	if len(text) > MaxOSC52 {
		return Result{}, errors.New("too long to copy through the terminal")
	}
	seq := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07"
	if e.Getenv("STY") != "" { // GNU screen: wrap in DCS, in chunks it accepts
		var b strings.Builder
		for i := 0; i < len(seq); i += 240 {
			b.WriteString("\x1bP" + seq[i:min(i+240, len(seq))] + "\x1b\\")
		}
		seq = b.String()
	}
	if _, err := io.WriteString(e.Terminal, seq); err != nil {
		return Result{}, err
	}
	return Result{Via: "OSC 52"}, nil
}

func tools(e Env) []tool {
	switch {
	case e.GOOS == "darwin":
		return []tool{{"pbcopy", nil}}
	case e.GOOS == "windows":
		return []tool{{"clip.exe", nil}}
	case e.Getenv("WSL_DISTRO_NAME") != "":
		// clip.exe mangles UTF-8; PowerShell reads stdin as UTF-8 with this setting
		return []tool{{"powershell.exe", []string{"-NoProfile", "-Command",
			"[Console]::InputEncoding=[Text.Encoding]::UTF8; Set-Clipboard -Value ([Console]::In.ReadToEnd())"}}}
	}
	var out []tool
	if e.Getenv("WAYLAND_DISPLAY") != "" {
		out = append(out, tool{"wl-copy", nil})
	}
	if e.Getenv("DISPLAY") != "" {
		out = append(out, tool{"xclip", []string{"-selection", "clipboard"}}, tool{"xsel", []string{"--clipboard", "--input"}})
	}
	return out
}

// run feeds stdin to a command. Its output is not captured: xclip stays in the background to
// serve the clipboard, and waiting on its output pipes would hang.
func run(ctx context.Context, name string, args []string, stdin string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stdout, cmd.Stderr = nil, nil
	return cmd.Run()
}
