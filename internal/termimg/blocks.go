package termimg

import (
	"image"
	"image/color"
	"strconv"
	"strings"

	"golang.org/x/image/draw"
)

// Blocks draws an image with text: each cell is "▀", its foreground the upper pixel and its
// background the lower one, so cols×rows cells show cols×(2·rows) pixels. It works in any
// terminal with colour; truecolor uses 24-bit colours, otherwise the nearest of 256. Each line
// ends with the colours reset.
func Blocks(src image.Image, cols, rows int, truecolor bool) []string {
	if src == nil || cols <= 0 || rows <= 0 || src.Bounds().Empty() {
		return nil
	}
	dst := image.NewNRGBA(image.Rect(0, 0, cols, rows*2))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	out := make([]string, rows)
	var sb strings.Builder
	for r := range rows {
		sb.Reset()
		var lastFG, lastBG string
		for c := range cols {
			fg := colour(dst.NRGBAAt(c, 2*r), truecolor, true)
			bg := colour(dst.NRGBAAt(c, 2*r+1), truecolor, false)
			if fg != lastFG || bg != lastBG {
				sb.WriteString("\x1b[" + fg + ";" + bg + "m")
				lastFG, lastBG = fg, bg
			}
			sb.WriteString("▀")
		}
		sb.WriteString("\x1b[0m")
		out[r] = sb.String()
	}
	return out
}

// colour is the SGR parameters for p (alpha composited over white) as foreground or background.
func colour(p color.NRGBA, truecolor, fg bool) string {
	a := int(p.A)
	r := (int(p.R)*a + 255*(255-a)) / 255
	g := (int(p.G)*a + 255*(255-a)) / 255
	b := (int(p.B)*a + 255*(255-a)) / 255
	kind := "48"
	if fg {
		kind = "38"
	}
	if truecolor {
		return kind + ";2;" + strconv.Itoa(r) + ";" + strconv.Itoa(g) + ";" + strconv.Itoa(b)
	}
	return kind + ";5;" + strconv.Itoa(xterm256(r, g, b))
}

// xterm256 is the nearest colour of the standard 256-colour palette: the 6×6×6 cube (16-231, levels
// 0, 95, 135, 175, 215, 255) or the 24 greys (232-255, 8 to 238).
func xterm256(r, g, b int) int {
	level := func(v int) int {
		if v < 48 {
			return 0
		}
		if v < 115 {
			return 1
		}
		return (v - 35) / 40
	}
	val := func(i int) int {
		if i == 0 {
			return 0
		}
		return 55 + 40*i
	}
	ri, gi, bi := level(r), level(g), level(b)
	cube := 16 + 36*ri + 6*gi + bi
	cd := dist(r, g, b, val(ri), val(gi), val(bi))
	avg := (r + g + b) / 3
	gi2 := min(max((avg-3)/10, 0), 23)
	gv := 8 + 10*gi2
	if dist(r, g, b, gv, gv, gv) < cd {
		return 232 + gi2
	}
	return cube
}
