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
	group      string // frames of one live view: a newer frame replaces an older one
	row, col   int    // 1-based screen position of the top-left cell
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
	live    []ovRect // where live view frames were last drawn (see leftBehind)
	sixel   map[string]string
	pending map[string]bool   // Sixel pictures being encoded (off the UI's goroutine)
	latest  map[string]string // the newest Sixel picture made for each group and size
	nextPic int
	pics    map[string]int // file path -> marker number
	picPath map[int]string
}

func (o *overlay) add(it ovItem) { o.plan = append(o.plan, it) }

type ovRect struct{ row, col, cols, rows int }

func (r ovRect) covers(row, col int) bool {
	return row >= r.row && row < r.row+r.rows && col >= r.col && col < r.col+r.cols
}

// leftBehind reports whether a live view frame was drawn where the plan now has no picture, as when
// the strip closes or gets shorter. Sixel and iTerm2 pixels stay on the screen until the cells
// under them are written, and blank cells that stay blank never are.
func (o *overlay) leftBehind() bool {
	for _, old := range o.live {
		for r := old.row; r < old.row+old.rows; r++ {
			for c := old.col; c < old.col+old.cols; c++ {
				covered := false
				for _, it := range o.plan {
					if (ovRect{it.row, it.col, it.cols, it.rows}).covers(r, c) {
						covered = true
						break
					}
				}
				if !covered {
					return true
				}
			}
		}
	}
	return false
}

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
	if o.sig != o.drawn && len(o.live) > 0 && (m.img.caps.Proto == termimg.Sixel || m.img.caps.Proto == termimg.ITerm2) && o.leftBehind() {
		o.live, o.drawn = nil, ""
		return tea.Batch(tea.ClearScreen, m.ovSchedule(ovSettle)) // then the pictures still planned are drawn again
	}
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

type sixelReadyMsg struct{ key, group, seq string }

// encodeSixel makes a Sixel picture in the background: it costs too much for the UI's goroutine.
// A live view encodes one frame at a time; frames that come meanwhile are skipped.
func (m *model) encodeSixel(k string, it ovItem) tea.Cmd {
	o := &m.img.ov
	if o.pending == nil {
		o.pending = map[string]bool{}
	}
	busy := k
	if it.group != "" {
		busy = it.group
	}
	if o.pending[busy] {
		return nil
	}
	o.pending[busy] = true
	caps := m.img.caps
	group := sizeKey(it.group, it)
	return func() tea.Msg {
		img := it.img
		if img == nil && it.decode != nil {
			img, _ = it.decode()
		}
		if img == nil {
			return sixelReadyMsg{key: k, group: group}
		}
		return sixelReadyMsg{key: k, group: group, seq: caps.SixelFrom(img, it.cols, it.rows)}
	}
}

// sizeKey names a picture of group at the item's size ("" when it has no group).
func sizeKey(group string, it ovItem) string {
	if group == "" {
		return ""
	}
	return fmt.Sprintf("%s/%dx%d", group, it.cols, it.rows)
}

func (m *model) handleSixelReady(msg sixelReadyMsg) tea.Cmd {
	o := &m.img.ov
	delete(o.pending, msg.key)
	if msg.group != "" {
		delete(o.pending, msg.group[:strings.LastIndex(msg.group, "/")])
	}
	if msg.seq == "" {
		return nil
	}
	if o.sixel == nil || len(o.sixel) > 32 {
		o.sixel = map[string]string{}
	}
	o.sixel[msg.key] = msg.seq
	if msg.group != "" {
		if o.latest == nil || len(o.latest) > 8 {
			o.latest = map[string]string{}
		}
		o.latest[msg.group] = msg.seq
	}
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
	o.live = o.live[:0]
	for _, it := range o.plan {
		seq, cmd := m.ovSequence(it)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		if seq == "" {
			continue
		}
		if strings.HasPrefix(it.group, "live/") {
			o.live = append(o.live, ovRect{it.row, it.col, it.cols, it.rows})
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
		// Drawn when ready; a live view shows its newest finished frame meanwhile, so a page that
		// keeps changing still moves even when frames come faster than Sixel is made.
		return m.img.ov.latest[sizeKey(it.group, it)], m.encodeSixel(k, it)
	case termimg.Kitty:
		id := m.newImageID()
		m.img.ov.kitty = append(m.img.ov.kitty, id)
		return termimg.KittyShow(id, it.enc, it.cols, it.rows), nil
	}
	return "", nil
}
