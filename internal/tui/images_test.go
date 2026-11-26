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

func TestScreenshotLinkElsewhere(t *testing.T) {
	m := chatModel(t)
	var out bytes.Buffer
	m.img = gfx{caps: termimg.Caps{Proto: termimg.ITerm2}, out: &out}
	path, _ := writePNG(t, 640, 400)
	m.pushItem(session.Item{Kind: "tool", Call: "Screenshot", Text: "640×400", Image: path})
	v := m.View()
	if !strings.Contains(v, "image 640×400") || !strings.Contains(v, "/browser view") || out.Len() != 0 {
		t.Fatalf("link card wrong (wrote %d bytes):\n%s", out.Len(), v)
	}
	_ = os.Remove(path)
	m.img.imgs = nil
	m.entries[len(m.entries)-1].w = -1
	if !strings.Contains(m.View(), "no longer on disk") {
		t.Fatal("missing file not explained")
	}
}

func TestLiveViewDrawsAtAFixedPlace(t *testing.T) {
	m := chatModel(t)
	var out bytes.Buffer
	m.img = gfx{caps: termimg.Caps{Proto: termimg.ITerm2, CellW: 8, CellH: 16}, out: &out}
	m.prevState, m.state = stateChat, stateLive
	_, data := writePNG(t, 800, 600)
	m.handleLiveFrame(liveFrameMsg{data: data, url: "http://localhost:3000/", open: true})
	v := m.View()
	if !strings.Contains(v, "localhost:3000") || strings.Count(v, "\n") != m.h-1 {
		t.Fatalf("live screen layout:\n%s", v)
	}
	m.drawLive()
	s := out.String()
	if !strings.HasPrefix(s, "\x1b7\x1b[2;") || !strings.Contains(s, "\x1b]1337;File=") || !strings.HasSuffix(s, "\x1b8") {
		t.Fatalf("draw sequence: %q", s[:min(60, len(s))])
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.state != stateChat {
		t.Fatal("esc didn't leave the view")
	}
}
