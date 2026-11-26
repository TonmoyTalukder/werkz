package termimg

import (
	"bytes"
	"image"
	"image/png"
	"strconv"
	"strings"

	"golang.org/x/image/draw"
)

// Palette: 216-colour 6×6×6 cube (0..215) followed by 40 greys (216..255).
const (
	cubeColors = 216
	numGreys   = 40
)

func greyLevel(i int) int { return (i + 1) * 255 / (numGreys + 1) }

func paletteRGB(i int) (r, g, b int) {
	if i < cubeColors {
		return (i / 36) * 51, (i / 6 % 6) * 51, (i % 6) * 51
	}
	v := greyLevel(i - cubeColors)
	return v, v, v
}

func nearest(r, g, b int) int {
	clamp := func(v int) int { return max(0, min(255, v)) }
	r, g, b = clamp(r), clamp(g), clamp(b)
	q := func(v int) int { return (v + 25) / 51 }
	ci := q(r)*36 + q(g)*6 + q(b)
	cr, cg, cb := paletteRGB(ci)
	best, bestD := ci, dist(r, g, b, cr, cg, cb)
	gi := ((r+g+b)/3*(numGreys+1) + 127) / 255
	gi = max(1, min(numGreys, gi)) - 1
	gr, gg, gb := paletteRGB(cubeColors + gi)
	if d := dist(r, g, b, gr, gg, gb); d < bestD {
		best = cubeColors + gi
	}
	return best
}

func dist(r1, g1, b1, r2, g2, b2 int) int {
	return (r1-r2)*(r1-r2) + (g1-g2)*(g1-g2) + (b1-b2)*(b1-b2)
}

// SixelImage scales the PNG to cols*CellW × rows*CellH px (8×16 cells if
// unknown) and encodes it as Sixel with a fixed 256-colour palette,
// Floyd–Steinberg dithering and run-length encoding.
func (c Caps) SixelImage(data []byte, cols, rows int) (string, error) {
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	cw, ch := c.cell()
	w, h := max(1, cols*cw), max(1, rows*ch)
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)

	// Quantise with Floyd–Steinberg (alpha composited over white).
	idx := make([]uint8, w*h)
	cur := make([][3]float32, w+2)
	nxt := make([][3]float32, w+2)
	for y := range h {
		for x := range w {
			p := dst.NRGBAAt(x, y)
			a := int(p.A)
			ch3 := [3]int{
				(int(p.R)*a + 255*(255-a)) / 255,
				(int(p.G)*a + 255*(255-a)) / 255,
				(int(p.B)*a + 255*(255-a)) / 255,
			}
			e := cur[x+1]
			r := ch3[0] + int(e[0])
			g := ch3[1] + int(e[1])
			b := ch3[2] + int(e[2])
			n := nearest(r, g, b)
			idx[y*w+x] = uint8(n)
			pr, pg, pb := paletteRGB(n)
			er := [3]float32{float32(r - pr), float32(g - pg), float32(b - pb)}
			for k := range 3 {
				cur[x+2][k] += er[k] * 7 / 16
				nxt[x][k] += er[k] * 3 / 16
				nxt[x+1][k] += er[k] * 5 / 16
				nxt[x+2][k] += er[k] / 16
			}
		}
		cur, nxt = nxt, cur
		for i := range nxt {
			nxt[i] = [3]float32{}
		}
	}

	used := [256]bool{}
	for _, v := range idx {
		used[v] = true
	}
	var sb strings.Builder
	sb.WriteString("\x1bPq\"1;1;" + strconv.Itoa(w) + ";" + strconv.Itoa(h))
	for i, u := range used {
		if u {
			r, g, b := paletteRGB(i)
			sb.WriteString("#" + strconv.Itoa(i) + ";2;" + strconv.Itoa((r*100+127)/255) +
				";" + strconv.Itoa((g*100+127)/255) + ";" + strconv.Itoa((b*100+127)/255))
		}
	}
	masks := make([][]byte, 256)
	for y0 := 0; y0 < h; y0 += 6 {
		var inBand [256]bool
		for k := range 6 {
			if y0+k >= h {
				break
			}
			for x := range w {
				ci := idx[(y0+k)*w+x]
				if !inBand[ci] {
					inBand[ci] = true
					if masks[ci] == nil {
						masks[ci] = make([]byte, w)
					}
					clear(masks[ci])
				}
				masks[ci][x] |= 1 << k
			}
		}
		first := true
		for ci, ok := range inBand {
			if !ok {
				continue
			}
			if !first {
				sb.WriteByte('$')
			}
			first = false
			sb.WriteString("#" + strconv.Itoa(ci))
			writeRLE(&sb, masks[ci])
		}
		sb.WriteByte('-')
	}
	sb.WriteString(st)
	return sb.String(), nil
}

func writeRLE(sb *strings.Builder, m []byte) {
	// Trailing empty columns need no data.
	n := len(m)
	for n > 0 && m[n-1] == 0 {
		n--
	}
	for i := 0; i < n; {
		j := i
		for j < n && m[j] == m[i] {
			j++
		}
		ch := byte('?') + m[i]
		if run := j - i; run > 3 {
			sb.WriteString("!" + strconv.Itoa(run))
			sb.WriteByte(ch)
		} else {
			for range run {
				sb.WriteByte(ch)
			}
		}
		i = j
	}
}
