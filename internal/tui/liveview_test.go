package tui

import (
	"bytes"
	"image"
	"image/color"
	"strings"
	"testing"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/agent"
	"github.com/TonmoyTalukder/werkz/internal/browser"
	"github.com/TonmoyTalukder/werkz/internal/termimg"

	tea "github.com/charmbracelet/bubbletea"
)

func browseStarted(m *model) {
	m.turn.active = true
	m.Update(agent.ToolCallMsg{ID: "1", Name: "Browse", Summary: "http://localhost:3000/"})
}

// The strip opens by itself when the model uses the browser, shows the page above the input
// without pushing the input away, and closes a moment after the turn.
func TestStripOpensAndCloses(t *testing.T) {
	m := chatModel(t)
	m.img = gfx{caps: termimg.Caps{TrueColor: true}, out: &bytes.Buffer{}}
	browseStarted(m)
	if !m.live.strip || m.live.stop == nil {
		t.Fatal("using the browser didn't open the strip")
	}
	defer m.unwatch()
	_, data := writePNG(t, 800, 500)
	fakeWatch(t, m, data, "http://localhost:3000/")
	m.Update(agent.ToolResultMsg{ID: "1", Name: "Browse", Summary: "Shop · 200"})
	v := m.View()
	lines := strings.Split(v, "\n")
	if len(lines) != m.h {
		t.Fatalf("view is %d lines, screen %d", len(lines), m.h)
	}
	if !strings.Contains(v, "◉ ") || !strings.Contains(v, "Shop · 200") || !strings.Contains(v, "last: Browse(") ||
		!strings.Contains(v, "ctrl+g full view") || strings.Count(v, "▀") < 100 {
		t.Fatalf("strip missing:\n%s", v)
	}
	if !strings.Contains(v, "/ for commands") {
		t.Fatal("the input was pushed off the screen")
	}
	gen := m.live.gen
	m.turn.active = false
	if cmd := m.browserTurnEnded(); cmd == nil {
		t.Fatal("the strip should close after the turn")
	}
	m.Update(stripHideMsg{gen})
	if m.live.strip || m.live.stop != nil {
		t.Fatal("strip still open (or frames still watched) after the turn")
	}
	if strings.Contains(m.View(), "ctrl+g full view") {
		t.Fatal("strip still drawn")
	}
}

// A newer browser call keeps the strip: an older close request is ignored.
func TestStripStaysForNewBrowsing(t *testing.T) {
	m := chatModel(t)
	m.img = gfx{caps: termimg.Caps{}, out: &bytes.Buffer{}}
	browseStarted(m)
	defer m.unwatch()
	old := m.live.gen
	m.Update(agent.ToolCallMsg{ID: "2", Name: "Click", Summary: "ref_3"})
	m.turn.active = false
	m.Update(stripHideMsg{old})
	if !m.live.strip {
		t.Fatal("an old close request closed the strip")
	}
}

func TestStripOff(t *testing.T) {
	m := chatModel(t)
	m.img = gfx{caps: termimg.Caps{}, out: &bytes.Buffer{}}
	m.browserCommand([]string{"view", "off"})
	if m.cfg.BrowserView != "off" {
		t.Fatal("setting not saved")
	}
	browseStarted(m)
	if m.live.strip || m.live.stop != nil {
		t.Fatal("strip opened although turned off")
	}
	m.browserCommand([]string{"view", "auto"})
	browseStarted(m)
	defer m.unwatch()
	if !m.live.strip {
		t.Fatal("strip didn't open after /browser view auto")
	}
}

// Approval prompts always fit: on a short screen the strip gives way.
func TestStripNeverHidesAPrompt(t *testing.T) {
	m := chatModel(t)
	m.img = gfx{caps: termimg.Caps{}, out: &bytes.Buffer{}}
	browseStarted(m)
	defer m.unwatch()
	_, data := writePNG(t, 800, 500)
	fakeWatch(t, m, data, "http://x/")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 22})
	fakeWatch(t, m, data, "http://x/")
	m.Update(agent.PermReqMsg{Tool: "Click", Summary: "submits the form", Reply: make(chan agent.PermDecision, 1)})
	v := m.View()
	if strings.Count(v, "\n") != m.h-1 {
		t.Fatalf("view taller than the screen:\n%s", v)
	}
	if !strings.Contains(v, "submits the form") {
		t.Fatalf("the prompt isn't fully shown:\n%s", v)
	}
}

// In iTerm2/WezTerm/Sixel terminals the strip leaves room and the picture is drawn into it.
func TestStripPictureDrawnInPlace(t *testing.T) {
	m := chatModel(t)
	var out bytes.Buffer
	m.img = gfx{caps: termimg.Caps{Proto: termimg.Sixel, CellW: 8, CellH: 16}, out: &out}
	browseStarted(m)
	defer m.unwatch()
	_, data := writePNG(t, 800, 500)
	fakeWatch(t, m, data, "http://x/")
	m.View()
	var it *ovItem
	for i := range m.img.ov.plan {
		if strings.HasPrefix(m.img.ov.plan[i].key, "live/strip/") {
			it = &m.img.ov.plan[i]
		}
	}
	if it == nil {
		t.Fatal("strip picture not planned")
	}
	if it.row != m.vp.Height+2 {
		t.Fatalf("planned at row %d, the strip's page starts at %d", it.row, m.vp.Height+2)
	}
	m.img.ov.at = time.Now().Add(-time.Second)
	runAll(m, m.handleOvTick()) // encodes in the background, then draws
	m.View()
	m.img.ov.at = time.Now().Add(-time.Second)
	m.handleOvTick()
	if s := out.String(); !strings.Contains(s, "\x1bPq") || !strings.Contains(s, "\x1b[") {
		t.Fatalf("no Sixel drawn: %q", s[:min(len(s), 40)])
	}
}

// Frames that come faster than Sixel is made: the newest finished one is drawn rather than
// waiting for one that is never ready in time (found in xterm on a page that kept changing).
func TestSixelKeepsUpWithFrames(t *testing.T) {
	m := chatModel(t)
	var out bytes.Buffer
	m.img = gfx{caps: termimg.Caps{Proto: termimg.Sixel, CellW: 8, CellH: 16}, out: &out}
	browseStarted(m)
	defer m.unwatch()
	_, data := writePNG(t, 800, 500)
	fakeWatch(t, m, data, "http://x/")
	m.View()
	m.img.ov.at = time.Now().Add(-time.Second)
	first := m.handleOvTick() // starts encoding frame 1
	if first == nil {
		t.Fatal("no encode started")
	}
	m.live.ver++ // frame 2 comes while frame 1 is being made
	m.View()
	m.img.ov.at = time.Now().Add(-time.Second)
	if c := m.handleOvTick(); c != nil {
		if _, ok := c().(sixelReadyMsg); ok {
			t.Fatal("a second Sixel encode started while one was running")
		}
	}
	runAll(m, first) // frame 1 is ready; frame 2's picture isn't
	out.Reset()
	m.img.ov.at = time.Now().Add(-time.Second)
	m.handleOvTick()
	if !strings.Contains(out.String(), "\x1bPq") {
		t.Fatal("the newest finished frame wasn't drawn")
	}
}

func TestDrawMark(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 400, 200))
	f := browser.Frame{ViewW: 800, ViewH: 400, Mark: &browser.Mark{Kind: "click", X: 400, Y: 200}}
	out := drawMark(img, f).(*image.NRGBA)
	if c := out.NRGBAAt(200, 100); c != markClick {
		t.Fatalf("no dot at the click point (scaled to the frame): %v", c)
	}
	if img.NRGBAAt(200, 100) != (color.NRGBA{}) {
		t.Fatal("the original frame was changed")
	}
	f.Mark = &browser.Mark{Kind: "type", X: 100, Y: 100, W: 200, H: 40}
	out = drawMark(img, f).(*image.NRGBA)
	if c := out.NRGBAAt(60, 48); c != markType {
		t.Fatalf("no frame around the field: %v", c)
	}
}

// runAll runs a command (and the commands of a batch) and hands their messages to the model.
func runAll(m *model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			runAll(m, c)
		}
	case sixelReadyMsg:
		m.handleSixelReady(msg)
	}
}

// A prompt that comes while the full view is open takes over the screen; frames stop if
// nothing else shows them.
func TestPromptLeavesFullView(t *testing.T) {
	m := chatModel(t)
	m.img = gfx{caps: termimg.Caps{}, out: &bytes.Buffer{}}
	_, data := writePNG(t, 800, 500)
	m.openLiveView()
	defer m.unwatch()
	fakeWatch(t, m, data, "http://x/")
	m.Update(agent.PermReqMsg{Tool: "Click", Summary: "submits the form", Reply: make(chan agent.PermDecision, 1)})
	if m.state != statePerm || m.live.stop != nil {
		t.Fatalf("state %v, frames still watched: %v", m.state, m.live.stop != nil)
	}
	if !strings.Contains(m.View(), "submits the form") {
		t.Fatal("prompt not shown")
	}
}

// Every row of a picture's room differs, so a moved room is always rewritten (no stale pixels).
func TestRoomRowsDiffer(t *testing.T) {
	m := chatModel(t)
	m.img = gfx{caps: termimg.Caps{Proto: termimg.ITerm2, CellW: 8, CellH: 16}, out: &bytes.Buffer{}}
	path, _ := writePNG(t, 640, 400)
	lines := strings.Split(m.renderImage(path, 100), "\n")
	seen := map[string]bool{}
	for _, l := range lines {
		if seen[l] {
			t.Fatalf("two room rows are the same: %q", l)
		}
		seen[l] = true
	}
	_, data := writePNG(t, 800, 500)
	fakeWatch(t, m, data, "http://x/")
	defer m.unwatch()
	rows := m.pageLines("strip", 40, 8, 10, 1)
	if rows[0] == rows[1] {
		t.Fatal("strip room rows are the same")
	}
}

// Inside tmux each kitty command goes through passthrough on its own (tmux drops long ones).
func TestTmuxWrapsEachKittyChunk(t *testing.T) {
	m := chatModel(t)
	var out bytes.Buffer
	m.img = gfx{caps: termimg.Caps{Proto: termimg.Kitty, Placeholders: true, InTmux: true}, out: &out}
	seq := termimg.KittyTransmit(1, 1, bytes.Repeat([]byte{7}, 20000), 10, 5)
	m.emit(seq)
	chunks := strings.Count(seq, "\x1b_G")
	if got := strings.Count(out.String(), "\x1bPtmux;"); got != chunks || chunks < 3 {
		t.Fatalf("%d passthrough wrappers for %d kitty commands", got, chunks)
	}
}
