package tui

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif" // MCP tools may return GIF and JPEG images
	_ "image/jpeg"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/TonmoyTalukder/werkz/internal/termimg"
)

// Screenshots in the terminal.
//
// In terminals with Kitty's Unicode placeholders (kitty, Ghostty) an image is uploaded once and
// then drawn by ordinary text cells, so it sits in the conversation and scrolls, reflows and
// redraws like text. Other terminals that can show images (iTerm2, WezTerm, Sixel terminals) get
// a link in the conversation and the full-screen live view (/browser view), where the image is
// drawn at a fixed place. Elsewhere, screenshots are links to the files.

const inlineMaxRows = 18 // an image in the conversation is at most this tall

type gfx struct {
	caps   termimg.Caps
	out    io.Writer // the terminal (tests replace it)
	nextID uint32
	imgs   map[string]*picture // by file path
	ov     overlay             // pictures drawn at a screen position (iTerm2, Sixel)
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
		// tmux drops a passthrough sequence that is too long: wrap each kitty command on its own
		var b strings.Builder
		for _, part := range strings.SplitAfter(seq, "\x1b\\") {
			if part != "" {
				b.WriteString(termimg.TmuxWrap(part))
			}
		}
		seq = b.String()
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
	label := "image ↗"
	if p.w > 0 {
		label = fmt.Sprintf("image %d×%d ↗", p.w, p.h)
	}
	link := "     " + sSky.Render("▣ ") + termimg.FileLink(path, sSky.Render(label))
	cols, rows := m.img.caps.Fit(p.w, p.h, max(w-6, 10), inlineMaxRows)
	switch {
	case m.inlineImages() && p.png:
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
	case m.pixelImages() && p.w > 0 && (p.png || m.img.caps.Proto != termimg.Kitty):
		// Room for the picture, which is drawn over it once all of it is on screen; until then the
		// link shows.
		n := m.markPicture(path)
		lines := make([]string, rows)
		lines[0] = "     " + picMarker(n) + sSky.Render("▣ ") + termimg.FileLink(path, sSky.Render(label))
		for i := 1; i < rows; i++ {
			lines[i] = roomLine("p"+strconv.Itoa(n), i)
		}
		return strings.Join(lines, "\n")
	}
	if m.img.caps.Proto != termimg.None {
		link += sDim.Render(" · /browser view shows the page here")
	}
	return link
}
