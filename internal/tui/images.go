package tui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif" // MCP tools may return GIF and JPEG images
	_ "image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/termimg"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Screenshots in the terminal.
//
// In terminals with Kitty's Unicode placeholders (kitty, Ghostty) an image is uploaded once and
// then drawn by ordinary text cells, so it sits in the conversation and scrolls, reflows and
// redraws like text. Other terminals that can show images (iTerm2, WezTerm, Sixel terminals) get
// a link in the conversation and the full-screen live view (/browser view), where the image is
// drawn at a fixed place. Elsewhere, screenshots are links to the files.

const (
	inlineMaxRows = 18 // an image in the conversation is at most this tall
	liveRefresh   = 1500 * time.Millisecond
)

type gfx struct {
	caps   termimg.Caps
	out    io.Writer // the terminal (tests replace it)
	nextID uint32
	imgs   map[string]*picture // by file path
	live   livePicture
}

type picture struct {
	id     uint32
	data   []byte
	w, h   int
	err    error
	placed map[[2]int]uint32 // cols×rows -> placement id (Kitty)
	nextP  uint32
	png    bool // a PNG (shown inline); other formats are links
}

// livePicture is the /browser view state.
type livePicture struct {
	data   []byte
	url    string
	at     time.Time
	err    error
	id     uint32
	shown  [2]int // cols×rows of the current Kitty placement
	placed uint32
}

type liveFrameMsg struct {
	data []byte
	url  string
	open bool
	err  error
}
type liveTickMsg struct{}
type liveDrawMsg struct{}

func (m *model) gfxOut() io.Writer {
	if m.img.out == nil {
		return os.Stdout
	}
	return m.img.out
}

// emit writes graphics escape sequences straight to the terminal: Kitty uploads don't move the
// cursor, so they can't disturb what the UI draws.
func (m *model) emit(seq string) {
	if m.img.caps.InTmux {
		seq = termimg.TmuxWrap(seq)
	}
	_, _ = io.WriteString(m.gfxOut(), seq)
}

func (m *model) inlineImages() bool {
	return m.img.caps.Proto == termimg.Kitty && m.img.caps.Placeholders
}

func (m *model) newImageID() uint32 {
	m.img.nextID++
	return 0x5A0000 + m.img.nextID // a recognisable range, clear of ids other programs pick
}

func (m *model) picture(path string) *picture {
	if m.img.imgs == nil {
		m.img.imgs = map[string]*picture{}
	}
	if p, ok := m.img.imgs[path]; ok {
		return p
	}
	p := &picture{placed: map[[2]int]uint32{}}
	p.data, p.err = os.ReadFile(path)
	if p.err == nil {
		// PNG shows inline (Kitty takes PNG); JPEG and GIF (MCP tools return those too) are links
		cfg, format, err := image.DecodeConfig(bytes.NewReader(p.data))
		if err == nil {
			p.w, p.h, p.png = cfg.Width, cfg.Height, format == "png"
		}
	}
	m.img.imgs[path] = p
	return p
}

// renderImage draws a screenshot under a tool result, sized to the width.
func (m *model) renderImage(path string, w int) string {
	p := m.picture(path)
	if p.err != nil {
		return sFaint.Render("     (image no longer on disk)")
	}
	if !m.inlineImages() || !p.png {
		label := "image ↗"
		if p.w > 0 {
			label = fmt.Sprintf("image %d×%d ↗", p.w, p.h)
		}
		s := "     " + sSky.Render("▣ ") + termimg.FileLink(path, sSky.Render(label))
		if m.img.caps.Proto != termimg.None {
			s += sDim.Render(" · /browser view shows the page here")
		}
		return s
	}
	cols, rows := m.img.caps.Fit(p.w, p.h, max(w-6, 10), inlineMaxRows)
	if p.id == 0 {
		p.id = m.newImageID()
	}
	key := [2]int{cols, rows}
	pl, ok := p.placed[key]
	if !ok {
		p.nextP++
		pl = p.nextP
		p.placed[key] = pl
		m.emit(termimg.KittyTransmit(p.id, pl, p.data, cols, rows))
	}
	lines := termimg.KittyPlaceholder(p.id, pl, cols, rows)
	return indent(strings.Join(lines, "\n"), "     ", "     ")
}

// ---------------------------------------------------------------- live view

func (m *model) openLiveView() tea.Cmd {
	if m.img.caps.Proto == termimg.None {
		return m.liveToFile()
	}
	m.prevState = m.state
	m.state = stateLive
	m.img.live.err = nil
	return tea.Batch(tea.ClearScreen, m.captureLive())
}

func (m *model) captureLive() tea.Cmd {
	eng := m.eng
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		data, url, open, err := eng.BrowserCapture(ctx)
		return liveFrameMsg{data: data, url: url, open: open, err: err}
	}
}

// liveToFile is /browser view where the terminal can't show images: save the page and link it.
func (m *model) liveToFile() tea.Cmd {
	eng := m.eng
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		data, url, open, err := eng.BrowserCapture(ctx)
		switch {
		case !open:
			return browserDoneMsg{text: "werkz's browser isn't open: nothing to show yet."}
		case err != nil:
			return browserDoneMsg{err: err}
		}
		dir, _ := os.UserCacheDir()
		path := filepath.Join(dir, "werkz", "screenshots", "live.png")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return browserDoneMsg{err: err}
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return browserDoneMsg{err: err}
		}
		return browserDoneMsg{text: "This terminal can't show images, so here is the page as a file: " +
			termimg.FileLink(path, tildify(path)) + " (" + url + ")"}
	}
}

func (m *model) handleLiveFrame(msg liveFrameMsg) tea.Cmd {
	if m.state != stateLive {
		return nil
	}
	if !msg.open {
		m.state = m.prevState
		return tea.Batch(tea.ClearScreen, m.printS(renderNotice("info", "werkz's browser isn't open: nothing to show yet.")))
	}
	l := &m.img.live
	l.err = msg.err
	if msg.err == nil {
		l.data, l.url, l.at = msg.data, msg.url, time.Now()
		if m.inlineImages() {
			m.transmitLive(true)
		}
	}
	next := tea.Tick(liveRefresh, func(time.Time) tea.Msg { return liveTickMsg{} })
	if m.inlineImages() {
		return next
	}
	return tea.Batch(next, m.drawLiveSoon())
}

// liveArea is where the live image goes: the whole screen below the header.
func (m *model) liveArea() (cols, rows int) {
	l := m.img.live
	cfg, err := png.DecodeConfig(bytes.NewReader(l.data))
	if err != nil {
		return 0, 0
	}
	return m.img.caps.Fit(cfg.Width, cfg.Height, max(m.w-2, 10), max(m.h-3, 4))
}

// transmitLive uploads the newest frame (Kitty): the same image id is replaced, so the
// placement shows it at once; a new size needs a new placement.
func (m *model) transmitLive(force bool) {
	l := &m.img.live
	if l.id == 0 {
		l.id = m.newImageID()
	}
	cols, rows := m.liveArea()
	if cols == 0 {
		return
	}
	if !force && l.shown == [2]int{cols, rows} {
		return
	}
	if l.shown != [2]int{cols, rows} {
		l.placed++
		l.shown = [2]int{cols, rows}
	}
	m.emit(termimg.KittyTransmit(l.id, l.placed, l.data, cols, rows))
}

// drawLiveSoon draws the frame (iTerm2, Sixel, Kitty without placeholders) just after the UI has
// drawn the empty area for it, so the UI never paints over it.
func (m *model) drawLiveSoon() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg { return liveDrawMsg{} })
}

func (m *model) drawLive() {
	l := m.img.live
	if m.state != stateLive || len(l.data) == 0 {
		return
	}
	cols, rows := m.liveArea()
	if cols == 0 {
		return
	}
	var seq string
	switch m.img.caps.Proto {
	case termimg.ITerm2:
		seq = termimg.ITerm2Image(l.data, cols, rows)
	case termimg.Sixel:
		s, err := m.img.caps.SixelImage(l.data, cols, rows)
		if err != nil {
			return
		}
		seq = s
	case termimg.Kitty: // no placeholders: upload, then place at the cursor
		if m.img.live.id == 0 {
			m.img.live.id = m.newImageID()
		}
		seq = termimg.KittyTransmit(m.img.live.id, 1, l.data, cols, rows) +
			fmt.Sprintf("\x1b_Ga=p,i=%d,p=2,c=%d,r=%d,C=1,q=2\x1b\\", m.img.live.id, cols, rows)
	default:
		return
	}
	left := max((m.w-cols)/2, 0)
	// save the cursor, go to the image area (row 2), draw, restore
	m.emit("\x1b7" + fmt.Sprintf("\x1b[2;%dH", left+1) + seq + "\x1b8")
}

func (m *model) liveKey(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "esc", "q", "enter":
		m.state = m.prevState
		if m.img.caps.Proto == termimg.Kitty && m.img.live.id != 0 {
			m.emit(termimg.KittyDelete(m.img.live.id))
			m.img.live.id, m.img.live.shown = 0, [2]int{}
		}
		return tea.ClearScreen // removes pixels drawn outside the text
	case "r":
		return m.captureLive()
	}
	return nil
}

// browserView is the full-screen /browser view.
func (m *model) browserView() string {
	l := m.img.live
	head := gradient("Browser", 0) + sDim.Render("  ·  ")
	switch {
	case l.err != nil:
		head += sRed.Render(l.err.Error())
	case l.url == "":
		head += sDim.Render("loading…")
	default:
		head += sText.Render(l.url) + sDim.Render("  ·  "+l.at.Format("15:04:05"))
	}
	head = ansi.Truncate(head, max(m.w-1, 10), "…")
	lines := []string{head}
	cols, rows := m.liveArea()
	if m.inlineImages() && cols > 0 && l.id != 0 {
		left := strings.Repeat(" ", max((m.w-cols)/2, 0))
		for _, ln := range termimg.KittyPlaceholder(l.id, l.placed, cols, rows) {
			lines = append(lines, left+ln)
		}
	}
	for len(lines) < m.h-1 {
		lines = append(lines, "")
	}
	lines = append(lines[:m.h-1], sDim.Render("live · refreshes every 1.5 s · r refresh now · esc back"))
	return strings.Join(lines, "\n")
}
