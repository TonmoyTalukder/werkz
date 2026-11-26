//go:build unix && !darwin

package clipboard

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// X11 clipboard without external tools. GNOME (and KDE) on Wayland run XWayland and share its
// clipboard with Wayland apps, so this also works on most Wayland desktops that lack wl-copy.
//
// An X11 clipboard is served by its owner for as long as it lives, so copying starts a small
// helper (werkz itself, with ServeArg) that owns the clipboard until something else is copied,
// the way xclip and wl-copy do. The text then stays pasteable after werkz quits.

// ServeArg is the hidden command-line argument that runs the clipboard helper.
const ServeArg = "__werkz-clipboard-x11"

// maxX11 is the most text the helper serves (one X request; larger copies would need INCR).
const maxX11 = 200 << 10

// x11Copy hands text to a new helper process and waits until it owns the clipboard.
func x11Copy(ctx context.Context, text string) error {
	if len(text) > maxX11 {
		return errors.New("too long for the X11 clipboard")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, ServeArg)
	cmd.Stdin = strings.NewReader(text)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // outlives werkz and its terminal
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // reap it whenever it ends
	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(out).ReadString('\n')
		ready <- strings.TrimSpace(line)
	}()
	select {
	case r := <-ready:
		if r != "ok" {
			if r == "" {
				r = "the clipboard helper stopped"
			}
			return errors.New(r)
		}
		return nil
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return errors.New("the X11 clipboard didn't answer")
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		return errors.New("the X11 clipboard didn't answer")
	}
}

// Serve is the clipboard helper: it reads the text from in, takes the X11 CLIPBOARD selection,
// reports "ok" (or the error) on status, and answers paste requests until another program takes
// the clipboard.
func Serve(in io.Reader, status io.Writer) error {
	data, err := io.ReadAll(io.LimitReader(in, maxX11+1))
	if err != nil {
		return report(status, err)
	}
	conn, err := xgb.NewConn()
	if err != nil {
		return report(status, err)
	}
	defer conn.Close()
	screen := xproto.Setup(conn).DefaultScreen(conn)
	win, err := xproto.NewWindowId(conn)
	if err != nil {
		return report(status, err)
	}
	if err := xproto.CreateWindowChecked(conn, 0, win, screen.Root, 0, 0, 1, 1, 0,
		xproto.WindowClassInputOnly, screen.RootVisual, 0, nil).Check(); err != nil {
		return report(status, err)
	}
	atom := func(name string) xproto.Atom {
		r, err := xproto.InternAtom(conn, false, uint16(len(name)), name).Reply()
		if err != nil {
			return 0
		}
		return r.Atom
	}
	clipboardA, targets := atom("CLIPBOARD"), atom("TARGETS")
	text := []xproto.Atom{atom("UTF8_STRING"), atom("text/plain;charset=utf-8"), atom("text/plain"), atom("STRING"), atom("TEXT")}
	if clipboardA == 0 || targets == 0 {
		return report(status, errors.New("the X server has no clipboard atoms"))
	}
	if err := xproto.SetSelectionOwnerChecked(conn, win, clipboardA, xproto.TimeCurrentTime).Check(); err != nil {
		return report(status, err)
	}
	if r, err := xproto.GetSelectionOwner(conn, clipboardA).Reply(); err != nil || r.Owner != win {
		return report(status, errors.New("couldn't take the clipboard"))
	}
	_ = report(status, nil)

	for {
		ev, err := conn.WaitForEvent()
		if err != nil {
			continue // a protocol error for one request; keep serving
		}
		if ev == nil {
			return nil // connection closed
		}
		switch e := ev.(type) {
		case xproto.SelectionClearEvent:
			return nil // someone else copied: our text is no longer the clipboard
		case xproto.SelectionRequestEvent:
			prop := e.Property
			if prop == 0 {
				prop = e.Target // old clients
			}
			switch {
			case e.Target == targets:
				list := append([]xproto.Atom{targets}, text...)
				buf := make([]byte, 4*len(list))
				for i, a := range list {
					xgb.Put32(buf[i*4:], uint32(a))
				}
				xproto.ChangeProperty(conn, xproto.PropModeReplace, e.Requestor, prop, xproto.AtomAtom, 32, uint32(len(list)), buf)
			case contains(text, e.Target):
				xproto.ChangeProperty(conn, xproto.PropModeReplace, e.Requestor, prop, e.Target, 8, uint32(len(data)), data)
			default:
				prop = 0 // a format we don't offer
			}
			notify := xproto.SelectionNotifyEvent{Time: e.Time, Requestor: e.Requestor, Selection: e.Selection, Target: e.Target, Property: prop}
			xproto.SendEvent(conn, false, e.Requestor, 0, string(notify.Bytes()))
		}
	}
}

func contains(list []xproto.Atom, a xproto.Atom) bool {
	for _, x := range list {
		if x == a {
			return true
		}
	}
	return false
}

func report(w io.Writer, err error) error {
	msg := "ok"
	if err != nil {
		msg = strings.ReplaceAll(err.Error(), "\n", " ")
	}
	_, _ = io.WriteString(w, msg+"\n")
	if c, ok := w.(io.Closer); ok {
		_ = c.Close() // werkz waits only for this line
	}
	return err
}
