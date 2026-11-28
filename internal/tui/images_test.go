package tui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/session"
	"github.com/TonmoyTalukder/werkz/internal/termimg"

	tea "github.com/charmbracelet/bubbletea"
)

func writePNG(t *testing.T, w, h int) (string, []byte) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		img.Set(x, h/2, color.RGBA{255, 0, 0, 255})
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	p := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(p, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p, b.Bytes()
}

func TestScreenshotInlineWithKitty(t *testing.T) {
	m := chatModel(t)
	var out bytes.Buffer
	m.img = gfx{caps: termimg.Caps{Proto: termimg.Kitty, Placeholders: true, CellW: 8, CellH: 16}, out: &out}
	path, _ := writePNG(t, 1280, 800)
	m.pushItem(session.Item{Kind: "tool", Call: "Screenshot", Text: "1280×800", Image: path})
	v := m.View()
	if !strings.Contains(out.String(), "\x1b_G") || !strings.Contains(out.String(), "U=1") {
		t.Fatalf("image not uploaded: %q", out.String()[:min(80, out.Len())])
	}
	if !strings.Contains(v, "\U0010EEEE") {
		t.Fatal("no placeholder cells in the conversation")
	}
	sent := out.Len()
	m.View() // same size: not uploaded again
	if out.Len() != sent {
		t.Fatal("re-uploaded on an unchanged redraw")
	}
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	m.View()
	if out.Len() == sent {
		t.Fatal("no new placement after a resize")
	}
}

// iTerm2, WezTerm and Sixel terminals get the picture in the conversation too: drawn over the
// room left for it once all of it is on screen, and only again when that room moved.
func TestScreenshotInlineWithITerm2(t *testing.T) {
	m := chatModel(t)
	var out bytes.Buffer
	m.img = gfx{caps: termimg.Caps{Proto: termimg.ITerm2, CellW: 8, CellH: 16}, out: &out}
	path, _ := writePNG(t, 640, 400)
	m.pushItem(session.Item{Kind: "tool", Call: "Screenshot", Text: "640×400", Image: path})
	v := m.View()
	if !strings.Contains(v, "image 640×400") || out.Len() != 0 {
		t.Fatalf("room for the picture wrong (wrote %d bytes):\n%s", out.Len(), v)
	}
	m.img.ov.at = time.Now().Add(-time.Second)
	m.handleOvTick()
	if !strings.Contains(out.String(), "\x1b]1337;File=") {
		t.Fatalf("picture not drawn: %q", out.String())
	}
	_ = os.Remove(path)
	m.img.imgs = nil
	m.entries[len(m.entries)-1].w = -1
	if !strings.Contains(m.View(), "no longer on disk") {
		t.Fatal("missing file not explained")
	}
}

// Terminals without pictures get a link to the file.
func TestScreenshotLinkElsewhere(t *testing.T) {
	m := chatModel(t)
	var out bytes.Buffer
	m.img = gfx{caps: termimg.Caps{}, out: &out}
	path, _ := writePNG(t, 640, 400)
	m.pushItem(session.Item{Kind: "tool", Call: "Screenshot", Text: "640×400", Image: path})
	if v := m.View(); !strings.Contains(v, "image 640×400") || out.Len() != 0 {
		t.Fatalf("link wrong:\n%s", v)
	}
}

// fakeWatch makes the model believe frames are coming and hands it one.
func fakeWatch(t *testing.T, m *model, data []byte, url string) {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	m.live.stop, m.live.done, m.live.ch = func() {}, done, make(chan liveFrame, 1)
	m.handleLiveFrame(liveFrameMsg{f: liveFrame{img: img, w: img.Bounds().Dx(), h: img.Bounds().Dy(), enc: data, url: url, at: time.Now()}, done: done})
}

func TestLiveViewDrawsAtAFixedPlace(t *testing.T) {
	m := chatModel(t)
	var out bytes.Buffer
	m.img = gfx{caps: termimg.Caps{Proto: termimg.ITerm2, CellW: 8, CellH: 16}, out: &out}
	m.prevState, m.state = stateChat, stateLive
	_, data := writePNG(t, 800, 600)
	fakeWatch(t, m, data, "http://localhost:3000/")
	v := m.View()
	if !strings.Contains(v, "localhost:3000") || strings.Count(v, "\n") != m.h-1 {
		t.Fatalf("live screen layout:\n%s", v)
	}
	m.img.ov.at = time.Now().Add(-time.Second) // the screen has caught up
	m.handleOvTick()
	s := out.String()
	if !strings.HasPrefix(s, "\x1b7\x1b[2;") || !strings.Contains(s, "\x1b]1337;File=") || !strings.HasSuffix(s, "\x1b8") {
		t.Fatalf("draw sequence: %q", s[:min(60, len(s))])
	}
	out.Reset()
	m.View()
	m.handleOvTick()
	if out.Len() != 0 {
		t.Fatal("drew again although nothing on the screen changed")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.state != stateChat {
		t.Fatal("esc didn't leave the view")
	}
}
