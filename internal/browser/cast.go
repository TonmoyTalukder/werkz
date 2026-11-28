package browser

import (
	"context"
	"hash/fnv"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// Live frames for the terminal's browser view.
//
// Chrome's screencast sends a frame only when the page's pixels change, so a still page costs
// nothing, and frames keep coming while an action runs (typing, a page loading). werkz acks
// each frame no sooner than the frame interval, which is how Chrome is kept to that rate.

// Frame is one picture of werkz's active tab.
type Frame struct {
	PNG []byte // PNG or JPEG image data (see WatchOptions.JPEG)
	// CSS size of the page area the frame shows, to place marks drawn on it.
	ViewW, ViewH float64
	URL          string
	Mark         *Mark // what the last action pointed at, while it is fresh
	At           time.Time
}

// Mark is where an action happened, in CSS pixels of the viewport.
type Mark struct {
	Kind       string // "click" or "type"
	X, Y, W, H float64
	At         time.Time
}

// MarkFor is how long a mark stays on the frames after its action.
const MarkFor = 1500 * time.Millisecond

type caster struct {
	mu       sync.Mutex
	fn       func(Frame)
	every    time.Duration
	maxW     int
	maxH     int
	jpeg     bool
	tab      *tab // the tab being cast
	lastAck  time.Time
	lastSeen time.Time // when the newest frame arrived
	stopOnce sync.Once
	pollGap  time.Duration // how long a still page waits for the next picture of its own
	lastPoll time.Time
	lastHash uint64
	last     *Frame // the newest frame, to send again without its mark once the mark is old
	stop     chan struct{}
}

// WatchOptions say how live frames are made.
type WatchOptions struct {
	FPS        int  // at most this many frames a second (default 8)
	MaxW, MaxH int  // frame size limit in pixels
	JPEG       bool // JPEG frames (cheaper to make) instead of PNG
}

// Watch sends frames of werkz's active tab to fn, at most fps a second and only when the page
// changes, each at most maxW×maxH pixels, until the returned func is called. Switching tabs is
// followed. Only one watcher runs; a new Watch replaces the old one.
func (b *Browser) Watch(o WatchOptions, fn func(Frame)) (stop func()) {
	if o.FPS <= 0 {
		o.FPS = 8
	}
	c := &caster{fn: fn, every: time.Second / time.Duration(o.FPS), maxW: max(o.MaxW, 64), maxH: max(o.MaxH, 64),
		jpeg: o.JPEG, stop: make(chan struct{}), pollGap: time.Second}
	b.castMu.Lock()
	old := b.cast
	b.cast = c
	b.castMu.Unlock()
	if old != nil {
		old.halt(b)
	}
	go b.castLoop(c)
	var once sync.Once
	return func() {
		once.Do(func() {
			b.castMu.Lock()
			if b.cast == c {
				b.cast = nil
			}
			b.castMu.Unlock()
			c.halt(b)
		})
	}
}

func (c *caster) halt(b *Browser) {
	first := false
	c.stopOnce.Do(func() { close(c.stop); first = true })
	if !first {
		return
	}
	c.mu.Lock()
	t := c.tab
	c.tab = nil
	c.mu.Unlock()
	if t != nil && t.ctx.Err() == nil {
		ctx, cancel := context.WithTimeout(t.ctx, 2*time.Second)
		defer cancel()
		_, _ = chromedp.Call(ctx, page.StopScreencast, cdp.Empty{})
	}
}

// castLoop keeps the screencast on the active tab. When the page sends nothing for a while (a
// window covered by others, which Chrome doesn't paint), it takes a picture itself about once a
// second when no action is running.
func (b *Browser) castLoop(c *caster) {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-tick.C:
		}
		// A relaunch (/browser show or hide) closes Chrome for a moment and opens a new tab: wait
		// for it rather than stopping; whoever watches stops the loop.
		b.mu.Lock()
		cur, closed := b.cur, b.closed
		b.mu.Unlock()
		if closed || cur == nil {
			continue
		}
		c.mu.Lock()
		casting := c.tab
		quiet := time.Since(c.lastSeen) > time.Second // no screencast frame for a while
		c.mu.Unlock()
		if cur != nil && cur != casting && !cur.gone.Load() {
			if casting != nil && casting.ctx.Err() == nil {
				ctx, cancel := context.WithTimeout(casting.ctx, 2*time.Second)
				_, _ = chromedp.Call(ctx, page.StopScreencast, cdp.Empty{})
				cancel()
			}
			c.mu.Lock()
			c.tab, c.lastSeen, c.pollGap = cur, time.Now(), time.Second // frames from now on are this tab's
			c.mu.Unlock()
			ctx, cancel := context.WithTimeout(cur.ctx, 5*time.Second)
			p := page.StartScreencastParams{Format: page.StartScreencastFormatPng, MaxWidth: int64(c.maxW), MaxHeight: int64(c.maxH), EveryNthFrame: 1, MaxFramesInFlight: 1}
			if c.jpeg {
				q := int64(80)
				p.Format, p.Quality = page.StartScreencastFormatJpeg, &q
			}
			_, err := chromedp.Call(ctx, page.StartScreencast, p)
			cancel()
			select {
			case <-c.stop: // stopped while starting: halt may have missed this screencast
				stopCast(cur)
				return
			default:
			}
			if err != nil {
				c.mu.Lock()
				c.tab = nil // try again on the next tick
				c.mu.Unlock()
			}
			continue
		}
		c.mu.Lock()
		last := c.last
		c.mu.Unlock()
		if last != nil && last.Mark != nil && time.Since(last.Mark.At) > MarkFor {
			f := *last
			f.Mark = nil
			b.deliver(c, f) // the page is still: take the old mark off
		}
		if quiet && time.Since(c.lastPoll) >= c.pollGap {
			b.pollFrame(c)
		}
	}
}

// pollFrame takes a picture when no action runs, for windows Chrome doesn't paint (covered by
// others). While the picture stays the same it looks less and less often (up to every 8 s).
func (b *Browser) pollFrame(c *caster) {
	if !b.opMu.TryLock() {
		return // an action runs: its own frames or the next poll show it
	}
	b.mu.Lock()
	tab := b.tab
	b.mu.Unlock()
	var data []byte
	var vw, vh float64
	if tab != nil {
		ctx, cancel := context.WithTimeout(tab, 2*time.Second)
		params := page.CaptureScreenshotParams{Format: page.CaptureScreenshotFormatJpeg, Quality: new(int64(80))}
		if !c.jpeg {
			params = page.CaptureScreenshotParams{Format: page.CaptureScreenshotFormatPng}
		}
		if v, err := b.viewport(ctx); err == nil && v.w > 0 && v.h > 0 {
			// One image pixel per CSS pixel at most, then down to the frame size limit, like the
			// screencast's frames.
			dpr := v.dpr
			if dpr <= 0 {
				dpr = 1
			}
			params.Clip = &page.Viewport{X: v.pageX, Y: v.pageY, Width: v.w, Height: v.h,
				Scale: min(1, float64(c.maxW)/v.w, float64(c.maxH)/v.h) / dpr}
			vw, vh = v.w, v.h
			if res, err := chromedp.Call(ctx, page.CaptureScreenshot, params); err == nil {
				data = res.Data
			}
		}
		cancel()
	}
	b.opMu.Unlock()
	h := fnv.New64a()
	_, _ = h.Write(data)
	c.mu.Lock()
	c.lastPoll = time.Now()
	same := h.Sum64() == c.lastHash
	c.lastHash = h.Sum64()
	if same {
		c.pollGap = min(c.pollGap*2, 8*time.Second)
	} else {
		c.pollGap = time.Second
	}
	c.mu.Unlock()
	if len(data) > 0 && !same {
		b.deliver(c, Frame{PNG: data, ViewW: vw, ViewH: vh})
	}
}

func stopCast(t *tab) {
	if t == nil || t.ctx.Err() != nil {
		return
	}
	ctx, cancel := context.WithTimeout(t.ctx, 2*time.Second)
	defer cancel()
	_, _ = chromedp.Call(ctx, page.StopScreencast, cdp.Empty{})
}

// onCastFrame takes one screencast frame of tab t.
func (b *Browser) onCastFrame(t *tab, e *page.EventScreencastFrame) {
	b.castMu.Lock()
	c := b.cast
	b.castMu.Unlock()
	ack := func() {
		ctx, cancel := context.WithTimeout(t.ctx, 2*time.Second)
		defer cancel()
		_, _ = chromedp.Call(ctx, page.ScreencastFrameAck, page.ScreencastFrameAckParams{SessionID: e.SessionID})
	}
	if c == nil {
		stopCast(t) // nobody watches: a screencast left running is stopped
		return
	}
	c.mu.Lock()
	mine := c.tab == t
	wait := c.every - time.Since(c.lastAck)
	c.lastSeen = time.Now()
	c.mu.Unlock()
	if !mine {
		ack()
		return
	}
	// Every frame is passed on (the last of a burst shows the page as it ends); one frame is in
	// flight at a time and the ack waits, which keeps Chrome to the frame rate.
	if len(e.Data) > 0 {
		f := Frame{PNG: e.Data}
		if m := e.Metadata; m != nil {
			f.ViewW, f.ViewH = m.DeviceWidth, m.DeviceHeight
		}
		b.deliver(c, f)
	}
	// The next frame comes only after this ack: waiting here is what caps the frame rate.
	select {
	case <-c.stop:
	case <-time.After(max(wait, 0)):
	}
	c.mu.Lock()
	c.lastAck = time.Now()
	c.mu.Unlock()
	ack()
}

func (b *Browser) deliver(c *caster, f Frame) {
	b.mu.Lock()
	f.URL = b.lastURL
	if m := b.mark; m != nil && time.Since(m.At) < MarkFor {
		cp := *m
		f.Mark = &cp
	}
	b.mu.Unlock()
	f.At = time.Now()
	select {
	case <-c.stop:
		return
	default:
	}
	c.mu.Lock()
	cp := f
	c.last = &cp
	c.mu.Unlock()
	c.fn(f)
}

// setMark records where an action happened, for the live view. Callers don't hold b.mu.
func (b *Browser) setMark(kind string, x, y, w, h float64) {
	b.mu.Lock()
	b.mark = &Mark{Kind: kind, X: x, Y: y, W: w, H: h, At: time.Now()}
	b.mu.Unlock()
}

// markBox marks a node's box (the field being typed into).
func (b *Browser) markBox(ctx context.Context, kind string, id cdp.BackendNodeID) {
	q, err := chromedp.Call(ctx, dom.GetContentQuads, dom.GetContentQuadsParams{BackendNodeID: id})
	if err != nil {
		return
	}
	for _, quad := range q.Quads {
		if len(quad) >= 8 {
			x0, y0, x1, y1 := quad[0], quad[1], quad[0], quad[1]
			for i := 0; i < 8; i += 2 {
				x0, x1 = min(x0, quad[i]), max(x1, quad[i])
				y0, y1 = min(y0, quad[i+1]), max(y1, quad[i+1])
			}
			b.setMark(kind, x0, y0, x1-x0, y1-y0)
			return
		}
	}
}
