package tui

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"image"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/termimg"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Pictures drawn at a screen position (iTerm2, WezTerm, Sixel terminals, and kitty-protocol
// terminals without Unicode placeholders).
//
// The text leaves blank room for each picture; View notes where that room is on the screen
// (overlay.add), and a moment later, once the text is on the screen, the pictures are drawn there.
// The terminal paints text over a picture when those cells change, so a picture is drawn again
// only when its room moved or its cells were redrawn; pictures half scrolled out of view aren't
// drawn (their room shows a link instead).

type ovItem struct {
	key        string
	row, col   int // 1-based screen position of the top-left cell
	cols, rows int
	img        image.Image // decoded, if at hand
	enc        []byte      // the encoded picture
	decode     func() (image.Image, []byte)
	sum        uint64 // the text in its cells, to notice when it was redrawn
}

type overlay struct {
	plan    []ovItem
	sig     string    // the plan, as drawn when it equals drawn
	at      time.Time // when the plan was made
	drawn   string
	ticking bool
	kitty   []uint32 // images placed directly (kitty without placeholders), deleted before a redraw
	sixel   map[string]string
	pending map[string]bool // Sixel pictures being encoded (off the UI's goroutine)
	nextPic int
	pics    map[string]int // file path -> marker number
	picPath map[int]string
}

func (o *overlay) add(it ovItem) { o.plan = append(o.plan, it) }

// picMarker is an invisible mark (an empty hyperlink with an id) put where a picture's room
// starts in the conversation, so View can find where it is on the screen.
func picMarker(n int) string { return "\x1b]8;id=wzpic" + strconv.Itoa(n) + ";\x1b\\\x1b]8;;\x1b\\" }

var picMarkRe = regexp.MustCompile(`\x1b\]8;id=wzpic(\d+);`)

func (m *model) markPicture(path string) int {
	o := &m.img.ov
	if n, ok := o.pics[path]; ok {
		return n
	}
	if o.pics == nil {
		o.pics, o.picPath = map[string]int{}, map[int]string{}
	}
	o.nextPic++
	o.pics[path], o.picPath[o.nextPic] = o.nextPic, path
	return o.nextPic
}

// planPictures finds the conversation's pictures in the visible lines (screen rows from 1): only
// those whose whole room is visible are drawn.
func (m *model) planPictures(lines []string) {
	for i, l := range lines {
		loc := picMarkRe.FindStringSubmatchIndex(l)
		if loc == nil {
			continue
		}
		n, _ := strconv.Atoi(l[loc[2]:loc[3]])
		path, ok := m.img.ov.picPath[n]
		if !ok {
			continue
		}
		p := m.picture(path)
		if p.err != nil || p.w == 0 {
			continue
		}
		cols, rows := m.img.caps.Fit(p.w, p.h, max(m.w-6, 10), inlineMaxRows)
		if i+rows > len(lines) {
			continue // partly off screen
		}
		data := p.data
		m.img.ov.add(ovItem{key: "pic/" + path, row: i + 1, col: ansi.StringWidth(l[:loc[0]]) + 1, cols: cols, rows: rows,
			enc: data, decode: func() (image.Image, []byte) {
				img, _, err := image.Decode(bytes.NewReader(data))
				if err != nil {
					return nil, nil
				}
				return img, data
			}})
	}
}

// finishPlan records the plan View made; screen is the whole screen it drew.
func (m *model) finishPlan(screen []string) {
	o := &m.img.ov
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d|%dx%d|", m.state, m.w, m.h))
	for i := range o.plan {
		it := &o.plan[i]
		h := fnv.New64a()
		for r := it.row - 1; r < it.row-1+it.rows && r < len(screen); r++ {
			if r >= 0 {
				h.Write([]byte(screen[r]))
			}
		}
		it.sum = h.Sum64()
		fmt.Fprintf(&sb, "%s@%d,%d,%dx%d#%x;", it.key, it.row, it.col, it.cols, it.rows, it.sum)
	}
	if sig := sb.String(); sig != o.sig {
		o.sig, o.at = sig, time.Now()
	}
}

// ovTick draws the planned pictures once their room is on the screen.
func (m *model) handleOvTick() tea.Cmd {
	o := &m.img.ov
	o.ticking = false
	if o.sig == o.drawn || (len(o.plan) == 0 && len(o.kitty) == 0) {
		o.drawn = o.sig
		return nil
	}
	if time.Since(o.at) < ovSettle {
		return m.ovSchedule(ovSettle)
	}
	cmds := m.drawPlan()
	o.drawn = o.sig
	return tea.Batch(cmds...)
}

type sixelReadyMsg struct{ key, seq string }

// encodeSixel makes a Sixel picture in the background: it costs too much for the UI's goroutine.
func (m *model) encodeSixel(k string, it ovItem) tea.Cmd {
	o := &m.img.ov
	if o.pending == nil {
		o.pending = map[string]bool{}
	}
	if o.pending[k] {
		return nil
	}
	o.pending[k] = true
	caps := m.img.caps
	return func() tea.Msg {
		img := it.img
		if img == nil && it.decode != nil {
			img, _ = it.decode()
		}
		if img == nil {
			return sixelReadyMsg{key: k}
		}
		return sixelReadyMsg{key: k, seq: caps.SixelFrom(img, it.cols, it.rows)}
	}
}

func (m *model) handleSixelReady(msg sixelReadyMsg) tea.Cmd {
	o := &m.img.ov
	delete(o.pending, msg.key)
	if msg.seq == "" {
		return nil
	}
	if o.sixel == nil || len(o.sixel) > 32 {
		o.sixel = map[string]string{}
	}
	o.sixel[msg.key] = msg.seq
	o.drawn = "" // draw it now
	return m.ovSchedule(0)
}

// ovSchedule keeps the check going while there is something to draw.
func (m *model) ovSchedule(d time.Duration) tea.Cmd {
	if !m.pixelImages() || m.img.ov.ticking {
		return nil
	}
	m.img.ov.ticking = true
	return tea.Tick(d, func(time.Time) tea.Msg { return ovTickMsg{} })
}

func (m *model) drawPlan() []tea.Cmd {
	o := &m.img.ov
	var cmds []tea.Cmd
	var b strings.Builder
	if m.img.caps.Proto == termimg.Kitty {
		for _, id := range o.kitty {
			b.WriteString(termimg.KittyDelete(id))
		}
		o.kitty = o.kitty[:0]
	}
	for _, it := range o.plan {
		seq, cmd := m.ovSequence(it)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		if seq == "" {
			continue
		}
		// save the cursor, go to the picture's place, draw, restore
		fmt.Fprintf(&b, "\x1b7\x1b[%d;%dH%s\x1b8", it.row, it.col, seq)
	}
	if b.Len() > 0 {
		m.emit(b.String())
	}
	return cmds
}

func (m *model) ovSequence(it ovItem) (string, tea.Cmd) {
	switch m.img.caps.Proto {
	case termimg.ITerm2:
		return termimg.ITerm2Image(it.enc, it.cols, it.rows), nil
	case termimg.Sixel:
		k := fmt.Sprintf("%s/%dx%d", it.key, it.cols, it.rows)
		if s, ok := m.img.ov.sixel[k]; ok {
			return s, nil
		}
		return "", m.encodeSixel(k, it) // drawn when ready
	case termimg.Kitty:
		id := m.newImageID()
		m.img.ov.kitty = append(m.img.ov.kitty, id)
		return termimg.KittyShow(id, it.enc, it.cols, it.rows), nil
	}
	return "", nil
}
