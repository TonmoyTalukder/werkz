package tui

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/termimg"
)

// pageLike is a frame like a real page: a header bar, blocks of "text" and photos.
func pageLike(w, h int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := color.NRGBA{255, 255, 255, 255}
			switch {
			case y < h/12:
				c = color.NRGBA{17, 24, 39, 255}
			case y > h/3 && y < h*2/3 && x > w/20 && x < w/3:
				c = color.NRGBA{uint8(x), uint8(y), 180, 255}
			case (y/9)%3 == 0 && x > w/2 && x%7 != 0:
				c = color.NRGBA{60, 60, 70, 255}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

// TestFrameCostPerProtocol measures what drawing one frame costs in each terminal mode, for a
// full-screen view (138×45 cells) of a 1104×640 frame, and the frame rate that cost allows on one
// core. Run with -v to see the numbers (they go into docs/usage.md).
func TestFrameCostPerProtocol(t *testing.T) {
	if testing.Short() {
		t.Skip("measurement")
	}
	src := pageLike(1104, 640)
	var pb, jb bytes.Buffer
	_ = png.Encode(&pb, src)
	_ = jpeg.Encode(&jb, src, &jpeg.Options{Quality: 80})
	caps := termimg.Caps{CellW: 8, CellH: 16}
	cols, rows := 138, 40
	measure := func(name string, f func()) {
		f() // warm up
		const n = 10
		start := time.Now()
		for range n {
			f()
		}
		per := time.Since(start) / n
		t.Logf("%-28s %7.1f ms a frame → up to %3.0f fps on one core", name, float64(per.Microseconds())/1000, float64(time.Second)/float64(per))
	}
	measure("decode JPEG frame", func() { _, _, _ = image.Decode(bytes.NewReader(jb.Bytes())) })
	measure("decode PNG frame", func() { _, _, _ = image.Decode(bytes.NewReader(pb.Bytes())) })
	measure("kitty (PNG upload)", func() { _ = termimg.KittyTransmit(1, 1, pb.Bytes(), cols, rows) })
	measure("iTerm2 (JPEG inline)", func() { _ = termimg.ITerm2Image(jb.Bytes(), cols, rows) })
	measure("Sixel (encode)", func() { _ = caps.SixelFrom(src, cols, rows) })
	small := pageLike(552, 360) // what the text mode asks Chrome for
	measure("half blocks (text)", func() { _ = termimg.Blocks(small, cols, rows+5, true) })
	t.Logf("bytes sent a frame: kitty %d KB, iTerm2 %d KB, Sixel %d KB, text ~%d KB",
		len(termimg.KittyTransmit(1, 1, pb.Bytes(), cols, rows))/1024, len(termimg.ITerm2Image(jb.Bytes(), cols, rows))/1024,
		len(caps.SixelFrom(src, cols, rows))/1024, lenLines(termimg.Blocks(small, cols, rows+5, true))/1024)
}

func lenLines(ls []string) int {
	n := 0
	for _, l := range ls {
		n += len(l)
	}
	return n
}
