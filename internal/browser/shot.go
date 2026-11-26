package browser

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

const (
	pixelThreshold = 24    // per-channel difference below this is antialias noise
	maxShotHeight  = 16000 // full-page captures taller than this are cut
)

// Shot is a saved screenshot, optionally compared with the previous one of the
// same page.
type Shot struct {
	Path          string
	Width, Height int
	Compared      string          // earlier shot path, "" if none
	Changed       float64         // share of changed pixels, 0..1
	Box           image.Rectangle // bounding box of the change
}

// Screenshot saves a PNG of the viewport (or the full page). With compare it
// diffs against the previous shot of the same URL (path and query, not
// fragment).
func (b *Browser) Screenshot(call context.Context, fullPage, compare bool) (*Shot, error) {
	ctx, done, err := b.begin(call)
	if err != nil {
		return nil, err
	}
	defer done()

	params := page.CaptureScreenshotParams{Format: page.CaptureScreenshotFormatPng}
	if fullPage {
		m, err := chromedp.Call(ctx, page.GetLayoutMetrics, cdp.Empty{})
		if err != nil {
			return nil, err
		}
		if m.CSSContentSize != nil {
			h := m.CSSContentSize.Height
			if h > maxShotHeight {
				h = maxShotHeight
			}
			params.CaptureBeyondViewport = new(true)
			params.Clip = &page.Viewport{Width: m.CSSContentSize.Width, Height: h, Scale: 1}
		}
	}
	res, err := chromedp.Call(ctx, page.CaptureScreenshot, params)
	if err != nil {
		return nil, fmt.Errorf("screenshot failed: %w", err)
	}
	cur, _ := chromedp.Run(ctx, chromedp.Location())
	if err := os.MkdirAll(b.opts.ShotDir, 0o700); err != nil {
		return nil, fmt.Errorf("cannot create the screenshot directory: %w", err)
	}
	now := time.Now()
	name := fmt.Sprintf("%s-%03d-%s.png", now.Format("20060102-150405"), now.Nanosecond()/1e6, fileSafe(hostOf(cur)))
	path := filepath.Join(b.opts.ShotDir, name)
	if err := os.WriteFile(path, res.Data, 0o600); err != nil {
		return nil, fmt.Errorf("cannot save the screenshot: %w", err)
	}
	s := &Shot{Path: path}
	if cfg, err := png.DecodeConfig(bytes.NewReader(res.Data)); err == nil {
		s.Width, s.Height = cfg.Width, cfg.Height
	}

	key := shotKey(cur, fullPage)
	b.mu.Lock()
	prev := b.lastShots[key]
	b.lastShots[key] = path
	b.mu.Unlock()
	if compare && prev != "" {
		if old, err := os.ReadFile(prev); err == nil {
			if changed, box, ok := diffPNG(old, res.Data); ok {
				s.Compared, s.Changed, s.Box = prev, changed, box
			}
		}
	}
	return s, nil
}

// shotKey identifies "the same page" for comparisons: path and query, no
// fragment, and viewport vs full page kept apart since their sizes differ.
func shotKey(raw string, full bool) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	k := u.EscapedPath() + "?" + u.RawQuery
	if full {
		k += "#full"
	}
	return k
}

func fileSafe(s string) string {
	if s == "" {
		return "page"
	}
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '-' {
			return r
		}
		return '-'
	}, s)
}

func toRGBA(img image.Image) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok && r.Rect.Min == (image.Point{}) {
		return r
	}
	out := image.NewRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()))
	draw.Draw(out, out.Bounds(), img, img.Bounds().Min, draw.Src)
	return out
}

// diffPNG compares two PNGs. Where the sizes differ, the overlapping corner is
// compared pixel by pixel and everything outside it counts as changed.
func diffPNG(a, b []byte) (changed float64, box image.Rectangle, ok bool) {
	ia, err := png.Decode(bytes.NewReader(a))
	if err != nil {
		return 0, box, false
	}
	ib, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return 0, box, false
	}
	ra, rb := toRGBA(ia), toRGBA(ib)
	wa, ha, wb, hb := ra.Rect.Dx(), ra.Rect.Dy(), rb.Rect.Dx(), rb.Rect.Dy()
	ow, oh := min(wa, wb), min(ha, hb)
	total := max(wa, wb) * max(ha, hb)
	if total == 0 {
		return 0, box, true
	}
	n := 0
	for y := 0; y < oh; y++ {
		pa, pb := ra.Pix[y*ra.Stride:], rb.Pix[y*rb.Stride:]
		for x := 0; x < ow; x++ {
			i := x * 4
			if absDiff(pa[i], pb[i]) > pixelThreshold || absDiff(pa[i+1], pb[i+1]) > pixelThreshold ||
				absDiff(pa[i+2], pb[i+2]) > pixelThreshold || absDiff(pa[i+3], pb[i+3]) > pixelThreshold {
				n++
				p := image.Rect(x, y, x+1, y+1)
				if box.Empty() {
					box = p
				} else {
					box = box.Union(p)
				}
			}
		}
	}
	// Area only one image has counts as changed.
	n += max(wa, wb)*max(ha, hb) - ow*oh
	if wa != wb {
		box = unionRect(box, image.Rect(ow, 0, max(wa, wb), max(ha, hb)))
	}
	if ha != hb {
		box = unionRect(box, image.Rect(0, oh, max(wa, wb), max(ha, hb)))
	}
	return float64(n) / float64(total), box, true
}

func unionRect(a, b image.Rectangle) image.Rectangle {
	if a.Empty() {
		return b
	}
	return a.Union(b)
}

func absDiff(a, b uint8) uint8 {
	if a > b {
		return a - b
	}
	return b - a
}

// Capture returns a PNG of the viewport without saving it: the live view in the terminal.
func (b *Browser) Capture(call context.Context) ([]byte, error) {
	ctx, done, err := b.begin(call)
	if err != nil {
		return nil, err
	}
	defer done()
	res, err := chromedp.Call(ctx, page.CaptureScreenshot, page.CaptureScreenshotParams{Format: page.CaptureScreenshotFormatPng})
	if err != nil {
		return nil, fmt.Errorf("screenshot failed: %w", err)
	}
	return res.Data, nil
}
