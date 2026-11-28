package browser

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// The live view's frames: an animated page streams at about the frame rate asked for, never more;
// a still page sends (almost) nothing; marks show where a click went.
func TestWatchFrames(t *testing.T) {
	b := br(t)
	s := serve(t, html(`<style>#s{width:80px;height:80px;border:10px solid #ddd;border-top-color:#e44;border-radius:50%;
		animation:r 1s linear infinite}@keyframes r{to{transform:rotate(360deg)}}</style><div id=s></div><button>Go</button>`))
	snap, err := b.Navigate(ctx5(t), s.URL)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var frames []Frame
	stop := b.Watch(WatchOptions{FPS: 8, MaxW: 640, MaxH: 400}, func(f Frame) {
		mu.Lock()
		frames = append(frames, f)
		mu.Unlock()
	})
	time.Sleep(3 * time.Second)
	mu.Lock()
	n := len(frames)
	mu.Unlock()
	t.Logf("animated: %d frames in 3s", n)
	if n < 9 || n > 30 {
		t.Errorf("animated page: %d frames in 3 s, want about 24 (8 fps)", n)
	}
	if _, err := b.Click(ctx5(t), refOf(t, snap.Tree, "button", "Go"), Allow{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	mu.Lock()
	marked := false
	for _, f := range frames[n:] {
		if f.Mark != nil && f.Mark.Kind == "click" && f.ViewW > 0 {
			marked = true
		}
	}
	mu.Unlock()
	if !marked {
		t.Error("no frame carried the click's mark")
	}
	stop()
	mu.Lock()
	after := len(frames)
	mu.Unlock()
	time.Sleep(time.Second)
	mu.Lock()
	defer mu.Unlock()
	if len(frames) != after {
		t.Errorf("%d frames arrived after stop", len(frames)-after)
	}
}

// How soon an action shows in the live view: from the start of a click to the first frame that
// shows its effect. Logged for the docs; the check is a generous bound.
func TestActionToFrameLatency(t *testing.T) {
	b := br(t)
	s := serve(t, html(`<button onclick="document.body.style.background=document.body.style.background==='red'?'blue':'red'">Go</button>`))
	snap, err := b.Navigate(ctx5(t), s.URL)
	if err != nil {
		t.Fatal(err)
	}
	ref := refOf(t, snap.Tree, "button", "Go")
	got := make(chan time.Time, 64)
	stop := b.Watch(WatchOptions{FPS: 8, MaxW: 640, MaxH: 400, JPEG: true}, func(f Frame) { got <- time.Now() })
	defer stop()
	time.Sleep(time.Second) // the first frame of the page
	var lat []time.Duration
	for range 6 {
		for len(got) > 0 {
			<-got
		}
		start := time.Now()
		if _, err := b.Click(ctx5(t), ref, Allow{}); err != nil {
			t.Fatal(err)
		}
		select {
		case at := <-got:
			lat = append(lat, at.Sub(start))
		case <-time.After(3 * time.Second):
			t.Fatal("no frame after a click")
		}
		time.Sleep(400 * time.Millisecond)
	}
	slices.Sort(lat)
	t.Logf("action to frame: min %v, median %v, max %v (6 clicks)", lat[0].Round(time.Millisecond),
		lat[len(lat)/2].Round(time.Millisecond), lat[len(lat)-1].Round(time.Millisecond))
	if lat[len(lat)/2] > time.Second {
		t.Errorf("median action-to-frame latency %v", lat[len(lat)/2])
	}
}

const spinner = `<style>#s{width:80px;height:80px;border:10px solid #ddd;border-top-color:#e44;border-radius:50%;
	animation:r 1s linear infinite}@keyframes r{to{transform:rotate(360deg)}}</style><div id=s></div>`

func framesIn(t *testing.T, d time.Duration, count func() int) int {
	t.Helper()
	before := count()
	time.Sleep(d)
	return count() - before
}

// /browser show and hide relaunch Chrome with the page: the live view keeps getting frames.
func TestWatchAcrossShowHide(t *testing.T) {
	b := br(t)
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("no display for a visible window")
	}
	if testing.Short() {
		t.Skip("slow")
	}
	s := serve(t, html(spinner))
	if _, err := b.Navigate(ctx5(t), s.URL+"/spin"); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	n := 0
	count := func() int { mu.Lock(); defer mu.Unlock(); return n }
	stop := b.Watch(WatchOptions{FPS: 8, MaxW: 640, MaxH: 400}, func(Frame) { mu.Lock(); n++; mu.Unlock() })
	defer stop()
	if got := framesIn(t, 2*time.Second, count); got < 4 {
		t.Fatalf("headless: %d frames in 2 s", got)
	}
	for _, headless := range []bool{false, true} {
		if err := b.SetHeadless(ctx5(t), headless); err != nil {
			t.Fatalf("SetHeadless(%v): %v", headless, err)
		}
		if !strings.HasSuffix(b.URL(), "/spin") {
			t.Fatalf("page lost: %q", b.URL())
		}
		got := framesIn(t, 3*time.Second, count)
		t.Logf("headless=%v: %d frames in 3 s", headless, got)
		// a shown window that other windows cover isn't painted: then pictures come about once a second
		if want := map[bool]int{true: 8, false: 2}[headless]; got < want {
			t.Errorf("after SetHeadless(%v): only %d frames in 3 s", headless, got)
		}
	}
}

// Connected to the user's Chrome, the live view shows werkz's own tab; closing werkz's side
// leaves that Chrome running.
func TestWatchConnected(t *testing.T) {
	u := startUserChrome(t)
	s := serve(t, html(spinner))
	allowHook.Store(func(string) bool { return true })
	b, err := Connect(ctx5(t), u.port, Options{ShotDir: filepath.Join(t.TempDir(), "shots"), AllowHost: func(string) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Navigate(ctx5(t), s.URL+"/spin"); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	n, urls := 0, map[string]bool{}
	stop := b.Watch(WatchOptions{FPS: 8, MaxW: 640, MaxH: 400}, func(f Frame) {
		mu.Lock()
		n++
		urls[f.URL] = true
		mu.Unlock()
	})
	got := framesIn(t, 3*time.Second, func() int { mu.Lock(); defer mu.Unlock(); return n })
	stop()
	t.Logf("connected: %d frames in 3 s", got)
	if got < 4 {
		t.Errorf("connected: only %d frames in 3 s", got)
	}
	mu.Lock()
	for u := range urls {
		if u != "" && !strings.HasSuffix(u, "/spin") {
			t.Errorf("a frame of another tab: %s", u)
		}
	}
	mu.Unlock()
	_ = b.Close()
	if len(u.tabs()) == 0 {
		t.Fatal("the user's Chrome went away with werkz")
	}
}

// Two stops at once (a resize while the view restarts) must not close the stop channel twice.
func TestCasterHaltTwice(t *testing.T) {
	b := &Browser{}
	c := &caster{stop: make(chan struct{})}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); c.halt(b) }()
	}
	wg.Wait()
}
