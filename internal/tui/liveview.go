package tui

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/agent"
	"github.com/TonmoyTalukder/werkz/internal/browser"
	"github.com/TonmoyTalukder/werkz/internal/termimg"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// The browser's page in the terminal.
//
// While the model uses the browser, a strip above the input shows the page live; ctrl+g (or
// /browser view) opens it full screen. Frames come from Chrome's screencast: only when the page
// changes, at most frameRate a second, sized to the terminal, and none while nothing shows them.
// The point a click went to and the field being typed into are marked for a moment.
//
// How the page is drawn depends on the terminal: kitty and Ghostty draw it with text cells
// (Unicode placeholders), so it moves with the text; iTerm2, WezTerm and Sixel terminals get a
// picture drawn at its place on the screen after the text (see overlay); any other terminal gets
// a text rendering with half blocks in colour.

const (
	stripLinger = 2500 * time.Millisecond // the strip stays this long after the turn ends
	ovEvery     = 50 * time.Millisecond   // how often pictures drawn at a place are checked
	ovSettle    = 30 * time.Millisecond   // a layout this new may not be on the screen yet
)

// liveFrame is a frame ready to show.
type liveFrame struct {
	img  image.Image // decoded, when the terminal needs the pixels (nil for kitty and iTerm2)
	w, h int
	enc  []byte // what image protocols are sent: PNG (kitty) or the frame's own data
	url  string
	at   time.Time
	file string // the full picture on disk (text-mode preview), "" if not saved
}

type liveFrameMsg struct {
	f    liveFrame
	done chan struct{}
}

type stripHideMsg struct{ gen int }
type ovTickMsg struct{}

// liveState is the live view's state.
type liveState struct {
	stop   func()        // stops the frames; nil while nobody watches
	done   chan struct{} // closed with stop
	ch     chan liveFrame
	key    [3]int // width, height, format being watched
	frame  *liveFrame
	ver    int // counts frames
	strip  bool
	gen    int
	action string      // the last browser action, e.g. "Click(ref_3)"
	status string      // its result: the page title and what happened
	hinted bool        // the terminal's Hint was shown
	times  []time.Time // when recent frames arrived (the frame rate shown)
	kitty  map[string]*kittyRegion
	blocks map[blockKey][]string
}

type kittyRegion struct {
	id, placed uint32
	size       [2]int
	ver        int
}

type blockKey struct{ ver, cols, rows int }

// liveOn reports whether the strip opens by itself (the default; /browser view off turns it off).
func (m *model) liveOn() bool { return m.cfg.BrowserView != "off" }

// pixelImages reports a terminal whose pictures are drawn at a screen position.
func (m *model) pixelImages() bool {
	switch m.img.caps.Proto {
	case termimg.ITerm2, termimg.Sixel:
		return true
	case termimg.Kitty:
		return !m.img.caps.Placeholders
	}
	return false
}

// browserToolStarted shows the strip when the model uses the browser.
func (m *model) browserToolStarted(name, summary string) tea.Cmd {
	m.live.action = name + "(" + summary + ")"
	if !m.liveOn() {
		return nil
	}
	m.live.strip = true
	m.live.gen++
	var cmds []tea.Cmd
	if h := m.img.caps.Hint; h != "" && !m.live.hinted {
		m.live.hinted = true
		cmds = append(cmds, m.printS(renderNotice("info", h)))
	}
	return tea.Batch(append(cmds, m.watch())...)
}

// browserTurnEnded hides the strip a moment after the turn.
func (m *model) browserTurnEnded() tea.Cmd {
	if !m.live.strip {
		return nil
	}
	gen := m.live.gen
	return tea.Tick(stripLinger, func(time.Time) tea.Msg { return stripHideMsg{gen} })
}

func (m *model) handleStripHide(msg stripHideMsg) tea.Cmd {
	if msg.gen != m.live.gen || m.turn.active {
		return nil
	}
	m.live.strip = false
	if m.state != stateLive {
		m.unwatch()
	}
	return nil
}

// watchSize is the largest frame any view needs: the full screen, in pixels.
func (m *model) watchSize() (w, h int) {
	cw, ch := m.img.caps.CellW, m.img.caps.CellH
	if cw <= 0 || ch <= 0 {
		cw, ch = 8, 16
	}
	if m.img.caps.Proto == termimg.None {
		cw, ch = 4, 8 // the text preview shows 1×2 pixels a cell: a little more keeps it sharp and the saved picture readable
	}
	return min(max(m.w-2, 10)*cw, 1600), min(max(m.h-3, 4)*ch, 1000)
}

// watch starts the frames (or keeps them going if their size still fits).
func (m *model) watch() tea.Cmd {
	w, h := m.watchSize()
	proto := m.img.caps.Proto
	key := [3]int{w, h, int(proto)}
	if m.live.stop != nil && m.live.key == key {
		return nil
	}
	keep, ver := m.live.frame, m.live.ver
	m.unwatch()
	m.live.frame, m.live.ver = keep, ver // shown until the first new frame
	ch := make(chan liveFrame, 1)
	done := make(chan struct{})
	kittyPNG := proto == termimg.Kitty
	var saveTo string
	if proto == termimg.None {
		if dir, err := os.UserCacheDir(); err == nil {
			saveTo = filepath.Join(dir, "werkz", "screenshots", "live.png")
		}
	}
	var mu sync.Mutex
	var last uint64
	var saved time.Time
	fps := m.frameRate()
	// kitty and iTerm2 send the frame as it is: decoding is needed only to draw a mark on it
	needPixels := !(proto == termimg.Kitty || proto == termimg.ITerm2)
	m.live.stop = m.eng.WatchBrowser(browser.WatchOptions{FPS: fps, MaxW: w, MaxH: h, JPEG: !kittyPNG}, func(f browser.Frame) {
		mu.Lock()
		defer mu.Unlock()
		hs := fnv.New64a()
		_, _ = hs.Write(f.PNG)
		if f.Mark != nil {
			fmt.Fprintf(hs, "%v", *f.Mark)
		}
		if sum := hs.Sum64(); sum == last {
			return // nothing changed
		} else {
			last = sum
		}
		lf := liveFrame{enc: f.PNG, url: f.URL, at: f.At}
		if needPixels || f.Mark != nil || saveTo != "" {
			img, format, err := image.Decode(bytes.NewReader(f.PNG))
			if err != nil {
				return
			}
			lf.img = img
			if f.Mark != nil {
				lf.img = drawMark(img, f)
				lf.enc = encodeLike(lf.img, format)
			}
			lf.w, lf.h = lf.img.Bounds().Dx(), lf.img.Bounds().Dy()
		} else {
			cfg, _, err := image.DecodeConfig(bytes.NewReader(f.PNG))
			if err != nil {
				return
			}
			lf.w, lf.h = cfg.Width, cfg.Height
		}
		if saveTo != "" && time.Since(saved) > time.Second {
			var b bytes.Buffer
			if png.Encode(&b, lf.img) == nil && os.MkdirAll(filepath.Dir(saveTo), 0o700) == nil &&
				os.WriteFile(saveTo, b.Bytes(), 0o600) == nil {
				saved = time.Now()
			}
		}
		if !saved.IsZero() {
			lf.file = saveTo
		}
		select {
		case <-ch: // the UI hasn't taken the previous frame: this one replaces it
		default:
		}
		ch <- lf
	})
	m.live.done, m.live.key, m.live.ch = done, key, ch
	return waitLiveFrame(ch, done)
}

func waitLiveFrame(ch chan liveFrame, done chan struct{}) tea.Cmd {
	return func() tea.Msg {
		select {
		case f := <-ch:
			return liveFrameMsg{f: f, done: done}
		case <-done:
			return nil
		}
	}
}

// unwatch stops the frames and frees what kitty holds.
func (m *model) unwatch() {
	if m.live.stop == nil {
		return
	}
	m.live.stop()
	close(m.live.done)
	m.live.stop, m.live.done, m.live.ch = nil, nil, nil
	for _, r := range m.live.kitty {
		m.emit(termimg.KittyDelete(r.id))
	}
	m.live.kitty, m.live.blocks, m.live.frame = nil, nil, nil
	m.img.ov.drawn = "" // what was drawn at a place may be gone
}

func (m *model) handleLiveFrame(msg liveFrameMsg) tea.Cmd {
	if msg.done != m.live.done || m.live.stop == nil {
		return nil // from frames already stopped
	}
	f := msg.f
	m.live.frame = &f
	m.live.ver++
	now := time.Now()
	m.live.times = append(m.live.times, now)
	for len(m.live.times) > 0 && now.Sub(m.live.times[0]) > 2*time.Second {
		m.live.times = m.live.times[1:]
	}
	m.live.blocks = nil
	return waitLiveFrame(m.live.ch, msg.done)
}

// ---------------------------------------------------------------- regions

// region is where the page goes: cols×rows cells.
func (m *model) fitFrame(maxCols, maxRows int) (cols, rows int) {
	if m.live.frame == nil {
		return 0, 0
	}
	// Frames may be enlarged to fill the room (Fit alone never enlarges a picture).
	f := m.live.frame
	return m.img.caps.Fit(f.w*4, f.h*4, maxCols, maxRows)
}

// pageLines draws the frame into cols×rows cells for region name ("strip" or "full"). For
// terminals that draw pictures at a place it returns blank lines and asks overlay to draw there
// (row and col are 1-based screen positions).
func (m *model) pageLines(name string, cols, rows, row, col int) []string {
	f := m.live.frame
	if f == nil || cols <= 0 || rows <= 0 {
		return nil
	}
	switch {
	case m.inlineImages():
		if m.live.kitty == nil {
			m.live.kitty = map[string]*kittyRegion{}
		}
		r := m.live.kitty[name]
		if r == nil {
			r = &kittyRegion{id: m.newImageID()}
			m.live.kitty[name] = r
		}
		if r.size != [2]int{cols, rows} || r.ver != m.live.ver {
			if r.size != [2]int{cols, rows} {
				r.placed++
				r.size = [2]int{cols, rows}
			}
			r.ver = m.live.ver
			m.emit(termimg.KittyTransmit(r.id, r.placed, f.enc, cols, rows))
		}
		return termimg.KittyPlaceholder(r.id, r.placed, cols, rows)
	case m.pixelImages():
		m.img.ov.add(ovItem{key: fmt.Sprintf("live/%s/%d", name, m.live.ver), row: row, col: col, cols: cols, rows: rows,
			img: f.img, enc: f.enc})
		out := make([]string, rows)
		for i := range out {
			out[i] = roomLine(name, i) + strings.Repeat(" ", cols)
		}
		return out
	}
	k := blockKey{m.live.ver, cols, rows}
	if m.live.blocks == nil {
		m.live.blocks = map[blockKey][]string{}
	}
	if l, ok := m.live.blocks[k]; ok {
		return l
	}
	l := termimg.Blocks(f.img, cols, rows, m.img.caps.TrueColor)
	m.live.blocks[k] = l
	return l
}

// stripRows is how tall the strip is with the input area bottomH lines high (0 = no strip).
func (m *model) stripRows(bottomH int) int {
	if !m.live.strip || m.state == stateLive || m.live.stop == nil {
		return 0
	}
	for rows := min(max(m.h/3, 5), 12); rows >= 4; rows-- {
		if m.h-bottomH-(rows+1) >= 6 { // the conversation keeps at least 6 lines
			return rows
		}
	}
	return 0
}

// stripView is the live strip: a line about the page, then the page. top is its first screen row.
func (m *model) stripView(rows, top int) string {
	head := sSky.Render("◉ ") + gradient("browser", 0)
	if m.live.status != "" {
		head += sDim.Render(" · ") + sText.Render(m.live.status)
	}
	if f := m.live.frame; f != nil && f.url != "" {
		head += sDim.Render(" · " + f.url)
	}
	if m.live.action != "" {
		head += sDim.Render(" · last: ") + sText.Render(m.live.action)
	}
	head = ansi.Truncate(head, max(m.w-26, 10), "…") + sDim.Render(" · ctrl+g full view")
	if f := m.live.frame; f != nil && f.file != "" && m.img.caps.Proto == termimg.None {
		head += " " + termimg.FileLink(f.file, sSky.Render("picture ↗"))
	}
	lines := []string{ansi.Truncate(head, m.w, "…")}
	cols, r := m.fitFrame(max(m.w-4, 10), rows)
	left := max((m.w-cols)/2, 0)
	page := m.pageLines("strip", cols, r, top+1, left+1)
	if page == nil {
		lines = append(lines, sDim.Render("  waiting for the page…"))
	}
	pad := strings.Repeat(" ", left)
	for _, l := range page {
		lines = append(lines, pad+l)
	}
	for len(lines) < rows+1 {
		lines = append(lines, "")
	}
	return strings.Join(lines[:rows+1], "\n")
}

// ---------------------------------------------------------------- full screen

func (m *model) openLiveView() tea.Cmd {
	m.prevState = m.state
	m.state = stateLive
	m.img.ov.drawn = ""
	return tea.Batch(tea.ClearScreen, m.watch())
}

func (m *model) closeLiveView() tea.Cmd {
	m.state = m.prevState
	if m.state == stateLive {
		m.state = stateChat
	}
	return m.endFullView()
}

// leaveLive closes the full view (if open) before another screen takes over, such as a prompt.
func (m *model) leaveLive() tea.Cmd {
	if m.state != stateLive {
		return nil
	}
	m.state = stateChat
	return m.endFullView()
}

func (m *model) endFullView() tea.Cmd {
	if r := m.live.kitty["full"]; r != nil {
		m.emit(termimg.KittyDelete(r.id))
		delete(m.live.kitty, "full")
	}
	if !m.live.strip {
		m.unwatch()
	}
	m.img.ov.drawn = ""
	return tea.ClearScreen // removes pixels drawn outside the text
}

func (m *model) liveKey(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "esc", "q", "enter", "ctrl+g":
		return m.closeLiveView()
	case "r":
		m.unwatch()
		return m.watch()
	}
	return nil
}

// browserView is the full-screen view.
func (m *model) browserView() string {
	head := gradient("Browser", 0) + sDim.Render("  ·  ")
	f := m.live.frame
	switch {
	case f == nil:
		head += sDim.Render("waiting for the page… (it shows once werkz's browser is open)")
	default:
		if m.live.status != "" {
			head += sText.Render(m.live.status) + sDim.Render("  ·  ")
		}
		head += sText.Render(f.url) + sDim.Render("  ·  "+f.at.Format("15:04:05"))
	}
	head = ansi.Truncate(head, max(m.w-1, 10), "…")
	lines := []string{head}
	cols, rows := m.fitFrame(max(m.w-2, 10), max(m.h-3, 4))
	left := max((m.w-cols)/2, 0)
	pad := strings.Repeat(" ", left)
	for _, l := range m.pageLines("full", cols, rows, 2, left+1) {
		lines = append(lines, pad+l)
	}
	for len(lines) < m.h-1 {
		lines = append(lines, "")
	}
	foot := fmt.Sprintf("live · %d fps (only while the page changes)", m.liveFPS())
	if m.live.action != "" {
		foot += " · last: " + m.live.action
	}
	foot += " · r restart · esc back"
	if f != nil && f.file != "" && m.img.caps.Proto == termimg.None {
		foot += " · " + termimg.FileLink(f.file, "full picture ↗")
	}
	lines = append(lines[:m.h-1], sDim.Render(ansi.Truncate(foot, max(m.w-1, 10), "…")))
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------- marks

var (
	markClick = color.NRGBA{0xF9, 0x73, 0x16, 0xFF} // saffron
	markType  = color.NRGBA{0x3B, 0x82, 0xF6, 0xFF} // blue
)

// drawMark returns a copy of img with the frame's mark: a ring where a click went, a frame
// around the field being typed into.
func drawMark(img image.Image, f browser.Frame) image.Image {
	b := img.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), img, b.Min, draw.Src)
	sx, sy := 1.0, 1.0
	if f.ViewW > 0 && f.ViewH > 0 {
		sx, sy = float64(b.Dx())/f.ViewW, float64(b.Dy())/f.ViewH
	}
	mk := f.Mark
	thick := max(2, b.Dx()/320)
	switch mk.Kind {
	case "type":
		x0, y0 := int(mk.X*sx)-thick, int(mk.Y*sy)-thick
		x1, y1 := int((mk.X+mk.W)*sx)+thick, int((mk.Y+mk.H)*sy)+thick
		for t := 0; t < thick; t++ {
			rect(out, x0-t, y0-t, x1+t, y1+t, markType)
		}
	default:
		cx, cy := int(mk.X*sx), int(mk.Y*sy)
		r := max(8, b.Dx()/60)
		ring(out, cx, cy, r, thick, markClick)
		ring(out, cx, cy, max(r/4, 2), max(r/4, 2), markClick) // a dot in the middle
	}
	return out
}

func rect(img *image.NRGBA, x0, y0, x1, y1 int, c color.NRGBA) {
	for x := x0; x <= x1; x++ {
		img.SetNRGBA(x, y0, c)
		img.SetNRGBA(x, y1, c)
	}
	for y := y0; y <= y1; y++ {
		img.SetNRGBA(x0, y, c)
		img.SetNRGBA(x1, y, c)
	}
}

func ring(img *image.NRGBA, cx, cy, r, thick int, c color.NRGBA) {
	in := max(r-thick, 0)
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			d := (x-cx)*(x-cx) + (y-cy)*(y-cy)
			if d <= r*r && d >= in*in {
				img.SetNRGBA(x, y, c)
			}
		}
	}
}

// encodeLike encodes img in the frame's format (JPEG frames stay JPEG, the rest become PNG).
func encodeLike(img image.Image, format string) []byte {
	var b bytes.Buffer
	if format == "jpeg" {
		_ = jpeg.Encode(&b, img, &jpeg.Options{Quality: 85})
	} else {
		_ = png.Encode(&b, img)
	}
	return b.Bytes()
}

// isBrowserTool says which tool calls open the strip.
func isBrowserTool(name string) bool { return agent.IsBrowserTool(name) }

// viewMethod names how this terminal shows the page, for /browser.
func (m *model) viewMethod() string {
	switch c := m.img.caps; {
	case c.Proto == termimg.Kitty && c.Placeholders && c.InTmux:
		return "kitty graphics through tmux"
	case c.Proto == termimg.Kitty && c.Placeholders:
		return "kitty graphics"
	case c.Proto == termimg.Kitty:
		return "kitty graphics (placed)"
	case c.Proto == termimg.ITerm2:
		return "iTerm2 images"
	case c.Proto == termimg.Sixel:
		return "Sixel"
	case c.TrueColor:
		return "text (half blocks, 24-bit colour)"
	}
	return "text (half blocks, 256 colours)"
}

// liveFPS is the frame rate over the last two seconds.
func (m *model) liveFPS() int {
	n := 0
	for _, t := range m.live.times {
		if time.Since(t) <= 2*time.Second {
			n++
		}
	}
	return (n + 1) / 2
}

// frameRate is the most frames a second this terminal gets: what drawing a frame costs there
// (measured in framecost_test.go) decides it.
func (m *model) frameRate() int {
	switch c := m.img.caps; {
	case c.Proto == termimg.Kitty && c.Placeholders:
		return 15 // the frame is sent as it is and kitty draws it: cheap
	case c.Proto == termimg.Kitty, c.Proto == termimg.ITerm2:
		return 8
	case c.Proto == termimg.Sixel:
		return 4 // encoding Sixel costs most (about 0.1 s for a full screen)
	}
	return 5 // the text drawing rewrites many cells a frame
}

// roomLine marks row i of a picture's room so that no two rows on the screen are the same text:
// when the room moves, every one of its old rows changes, the terminal rewrites them and the
// old picture's pixels go (Bubble Tea leaves unchanged lines alone).
func roomLine(tag string, i int) string {
	return "\x1b]8;id=wzr" + tag + "." + strconv.Itoa(i) + ";\x1b\\\x1b]8;;\x1b\\"
}
