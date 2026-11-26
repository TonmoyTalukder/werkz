package termimg

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestFromEnv(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want Caps
	}{
		{"empty", nil, Caps{}},
		{"kitty id", map[string]string{"KITTY_WINDOW_ID": "1"}, Caps{Proto: Kitty, Placeholders: true}},
		{"kitty term", map[string]string{"TERM": "xterm-kitty"}, Caps{Proto: Kitty, Placeholders: true}},
		{"ghostty", map[string]string{"TERM_PROGRAM": "ghostty"}, Caps{Proto: Kitty, Placeholders: true}},
		{"ghostty res", map[string]string{"GHOSTTY_RESOURCES_DIR": "/x"}, Caps{Proto: Kitty, Placeholders: true}},
		{"wezterm", map[string]string{"TERM_PROGRAM": "WezTerm"}, Caps{Proto: ITerm2}},
		{"iterm", map[string]string{"TERM_PROGRAM": "iTerm.app"}, Caps{Proto: ITerm2}},
		{"iterm lc", map[string]string{"LC_TERMINAL": "iTerm2"}, Caps{Proto: ITerm2}},
		{"tmux", map[string]string{"TMUX": "/s", "KITTY_WINDOW_ID": "1"}, Caps{InTmux: true}},
		{"tmux forced", map[string]string{"TMUX": "/s", "WERKZ_IMAGES": "iterm2"}, Caps{Proto: ITerm2, InTmux: true}},
		{"forced sixel", map[string]string{"WERKZ_IMAGES": "sixel", "KITTY_WINDOW_ID": "1"}, Caps{Proto: Sixel}},
		{"forced kitty", map[string]string{"WERKZ_IMAGES": "kitty"}, Caps{Proto: Kitty, Placeholders: true}},
		{"off", map[string]string{"WERKZ_IMAGES": "off", "KITTY_WINDOW_ID": "1"}, Caps{}},
	}
	for _, c := range cases {
		if got := FromEnv(env(c.env)); got != c.want {
			t.Errorf("%s: got %+v want %+v", c.name, got, c.want)
		}
	}
}

func TestProtocolString(t *testing.T) {
	for p, s := range map[Protocol]string{None: "none", Kitty: "kitty", ITerm2: "iterm2", Sixel: "sixel"} {
		if p.String() != s {
			t.Errorf("%d: %q", p, p.String())
		}
	}
}

type slowReader struct{ data []byte }

func (r *slowReader) Read(b []byte) (int, error) {
	if len(r.data) == 0 {
		time.Sleep(time.Hour)
	}
	n := copy(b, r.data)
	r.data = r.data[n:]
	return n, nil
}

func TestProbeWith(t *testing.T) {
	var out bytes.Buffer
	res := probeWith(&slowReader{[]byte("\x1b_Gi=31;OK\x1b\\\x1b[6;18;9t\x1b[?62;c")}, &out, time.Second)
	if !res.kitty || res.sixel || res.cellW != 9 || res.cellH != 18 {
		t.Errorf("kitty: %+v", res)
	}
	if !strings.Contains(out.String(), "a=q") || !strings.HasSuffix(out.String(), "\x1b[c") {
		t.Errorf("query: %q", out.String())
	}

	res = probeWith(&slowReader{[]byte("\x1b[?62;4;9c")}, &out, time.Second)
	if res.kitty || !res.sixel {
		t.Errorf("sixel: %+v", res)
	}
	var c Caps
	mergeProbe(&c, res)
	if c.Proto != Sixel {
		t.Errorf("merge: %+v", c)
	}
	c = Caps{Proto: ITerm2}
	mergeProbe(&c, probeResult{kitty: true})
	if c.Proto != ITerm2 {
		t.Error("env must win")
	}

	// Split replies across reads.
	res = probeWith(&slowReader{[]byte("\x1b_Gi=31;OK\x1b\\\x1b[?6")}, &out, 50*time.Millisecond)
	if res.gotDA1 {
		t.Error("partial DA1")
	}

	start := time.Now()
	res = probeWith(&slowReader{}, &out, 60*time.Millisecond)
	if res.kitty || res.sixel || time.Since(start) > time.Second {
		t.Errorf("timeout: %+v %v", res, time.Since(start))
	}
}

func TestFit(t *testing.T) {
	c := Caps{CellW: 10, CellH: 20}
	check := func(c Caps, w, h, mc, mr, wc, wr int) {
		t.Helper()
		if cc, rr := c.Fit(w, h, mc, mr); cc != wc || rr != wr {
			t.Errorf("Fit(%d,%d,%d,%d)=%d,%d want %d,%d", w, h, mc, mr, cc, rr, wc, wr)
		}
	}
	check(c, 1000, 400, 40, 40, 40, 8)       // wide: width bound
	check(c, 400, 2000, 40, 10, 4, 10)       // tall: height bound
	check(c, 100, 100, 40, 40, 10, 5)        // small: native size
	check(Caps{}, 800, 800, 100, 10, 20, 10) // unknown cell → 8x16
	check(Caps{}, 0, 10, 10, 10, 0, 0)
	if cc, rr := c.Fit(1, 1, 5, 5); cc < 1 || rr < 1 {
		t.Error("minimum 1x1")
	}
}

func testPNG(t testing.TB, w, h int, noisy bool) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewSource(1))
	for y := range h {
		for x := range w {
			if noisy {
				img.Set(x, y, color.NRGBA{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), 255})
			} else {
				img.Set(x, y, color.NRGBA{uint8(x * 255 / w), uint8(y * 255 / h), 128, 255})
			}
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestKittyTransmit(t *testing.T) {
	data := testPNG(t, 100, 100, true)
	if len(data) < 20000 {
		t.Fatalf("png too small: %d", len(data))
	}
	s := KittyTransmit(7, 3, data, 30, 10)
	parts := strings.Split(strings.TrimSuffix(s, "\x1b\\"), "\x1b\\")
	if len(parts) < 4 {
		t.Fatalf("parts: %d", len(parts))
	}
	chunks, place := parts[:len(parts)-1], parts[len(parts)-1]
	if !strings.HasPrefix(chunks[0], "\x1b_Ga=t,f=100,q=2,i=7,m=1;") {
		t.Errorf("first: %.60q", chunks[0])
	}
	var b64 strings.Builder
	for i, c := range chunks {
		hdr, body, _ := strings.Cut(strings.TrimPrefix(c, "\x1b_G"), ";")
		if len(body) > 4096 {
			t.Errorf("chunk %d too big", i)
		}
		want := "m=1"
		if i == len(chunks)-1 {
			want = "m=0"
		}
		if !strings.HasSuffix(hdr, want) {
			t.Errorf("chunk %d header %q", i, hdr)
		}
		if i > 0 && hdr != want {
			t.Errorf("chunk %d extra keys %q", i, hdr)
		}
		b64.WriteString(body)
	}
	got, err := base64.StdEncoding.DecodeString(b64.String())
	if err != nil || !bytes.Equal(got, data) {
		t.Errorf("roundtrip failed: %v", err)
	}
	if place != "\x1b_Ga=p,U=1,i=7,p=3,c=30,r=10,q=2" {
		t.Errorf("placement: %q", place)
	}
	if !strings.Contains(KittyTransmit(7, 0, data[:10], 3, 1), "a=p,U=1,i=7,c=3,r=1,q=2") {
		t.Error("placement without p")
	}
	if KittyDelete(9) != "\x1b_Ga=d,d=I,i=9,q=2\x1b\\" {
		t.Errorf("delete: %q", KittyDelete(9))
	}
}

func TestDiacriticsTable(t *testing.T) {
	if len(diacritics) != 297 {
		t.Errorf("len = %d", len(diacritics))
	}
	if diacritics[0] != 0x305 || diacritics[1] != 0x30D {
		t.Error("table start")
	}
}

func TestKittyPlaceholder(t *testing.T) {
	id := uint32(0x010203)
	lines := KittyPlaceholder(id, 0, 6, 3)
	if len(lines) != 3 {
		t.Fatal(len(lines))
	}
	for r, l := range lines {
		if !strings.HasPrefix(l, "\x1b[38;2;1;2;3m") || !strings.HasSuffix(l, "\x1b[39m") {
			t.Errorf("colour: %q", l)
		}
		plain := ansi.Strip(l)
		if n := strings.Count(plain, string(placeholderRune)); n != 6 {
			t.Errorf("row %d: %d cells", r, n)
		}
		runes := []rune(plain)
		if len(runes) != 18 {
			t.Fatalf("runes %d", len(runes))
		}
		for c := range 6 {
			if runes[c*3] != placeholderRune || runes[c*3+1] != diacritics[r] || runes[c*3+2] != diacritics[c] {
				t.Errorf("cell %d,%d wrong", r, c)
			}
		}
		if w := ansi.StringWidth(l); w != 6 {
			t.Errorf("row %d: ansi.StringWidth = %d, want 6", r, w)
		}
		if strings.Contains(l, "\x1b[A") || strings.Contains(l, "\n") {
			t.Error("cursor movement")
		}
	}
	// One cell.
	t.Logf("ansi.StringWidth(single cell) = %d", ansi.StringWidth(KittyPlaceholder(1, 0, 1, 1)[0]))
	// Placement id in underline colour; high id byte as third diacritic.
	l := KittyPlaceholder(0x05000001, 0x000400, 2, 1)[0]
	if !strings.Contains(l, "\x1b[58;2;0;4;0m") || !strings.HasSuffix(l, "\x1b[39m\x1b[59m") {
		t.Errorf("placement colour: %q", l)
	}
	if ansi.StringWidth(l) != 2 {
		t.Errorf("width with high byte: %d", ansi.StringWidth(l))
	}
	if KittyPlaceholder(1, 0, 0, 3) != nil {
		t.Error("empty")
	}
}

func TestITerm2Image(t *testing.T) {
	s := ITerm2Image([]byte("abc"), 20, 5)
	want := "\x1b]1337;File=inline=1;size=3;width=20;height=5;preserveAspectRatio=1:YWJj\a"
	if s != want {
		t.Errorf("got %q", s)
	}
}

func TestFileLinkAndTmux(t *testing.T) {
	l := FileLink("/tmp/a b.png", "shot")
	re := regexp.MustCompile(`^\x1b\]8;;file://[^/]*/tmp/a%20b\.png\x1b\\shot\x1b\]8;;\x1b\\$`)
	if !re.MatchString(l) {
		t.Errorf("link %q", l)
	}
	if got := TmuxWrap("\x1b_Gx\x1b\\"); got != "\x1bPtmux;\x1b\x1b_Gx\x1b\x1b\\\x1b\\" {
		t.Errorf("tmux %q", got)
	}
}

// decodeSixel is a tiny decoder returning the size from raster attributes, the
// number of bands, the palette size and the widest band.
func decodeSixel(t *testing.T, s string) (w, h, bands, colours, maxX int) {
	t.Helper()
	if !strings.HasPrefix(s, "\x1bPq\"1;1;") || !strings.HasSuffix(s, "\x1b\\") {
		t.Fatalf("framing: %.20q", s)
	}
	body := strings.TrimSuffix(strings.TrimPrefix(s, "\x1bPq"), "\x1b\\")
	m := regexp.MustCompile(`^"1;1;(\d+);(\d+)`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no raster attrs")
	}
	w, h = atoi(m[1]), atoi(m[2])
	body = body[len(m[0]):]
	x := 0
	for i := 0; i < len(body); i++ {
		switch ch := body[i]; {
		case ch == '#':
			j := i + 1
			for j < len(body) && (body[j] >= '0' && body[j] <= '9' || body[j] == ';') {
				j++
			}
			if strings.Contains(body[i:j], ";2;") {
				colours++
			}
			i = j - 1
		case ch == '!':
			j := i + 1
			for body[j] >= '0' && body[j] <= '9' {
				j++
			}
			x += atoi(body[i+1 : j])
			i = j // skip data char
			maxX = max(maxX, x)
		case ch == '$':
			x = 0
		case ch == '-':
			x = 0
			bands++
		case ch >= '?' && ch <= '~':
			x++
			maxX = max(maxX, x)
		}
	}
	return
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func TestSixelImage(t *testing.T) {
	c := Caps{CellW: 8, CellH: 16}
	s, err := c.SixelImage(testPNG(t, 200, 100, false), 10, 3)
	if err != nil {
		t.Fatal(err)
	}
	w, h, bands, colours, maxX := decodeSixel(t, s)
	if w != 80 || h != 48 {
		t.Errorf("size %dx%d", w, h)
	}
	if bands != (h+5)/6 {
		t.Errorf("bands %d", bands)
	}
	if colours < 4 || colours > 256 {
		t.Errorf("colours %d", colours)
	}
	if maxX > w || maxX < w-2 {
		t.Errorf("maxX %d for width %d", maxX, w)
	}
	// Unknown cell size → 8x16; odd height gives ceil bands.
	s, err = (Caps{}).SixelImage(testPNG(t, 30, 30, false), 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, h, bands, _, _ := decodeSixel(t, s); h != 16 || bands != 3 {
		t.Errorf("h=%d bands=%d", h, bands)
	}
	if _, err := c.SixelImage([]byte("nope"), 1, 1); err == nil {
		t.Error("expected decode error")
	}
}

func TestNearestGrey(t *testing.T) {
	r, g, b := paletteRGB(nearest(130, 130, 130))
	if r != g || g != b || absInt(r-130) > 4 {
		t.Errorf("grey mapped to %d,%d,%d", r, g, b)
	}
	if n := nearest(255, 0, 0); n != 5*36 {
		t.Errorf("red -> %d", n)
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
