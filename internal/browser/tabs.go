package browser

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

const maxTabs = 8

// tab is one tab werkz owns. Only the active tab's page state lives in the
// Browser's own fields (refs, console, requests ...); the others keep theirs
// in st and swap in when the tab is switched to.
type tab struct {
	id   target.ID
	ctx  context.Context
	stop context.CancelFunc
	root bool // the browser's first tab: its context is the parent of the others
	gone atomic.Bool
	st   tabState
}

type tabState struct {
	mainID     cdp.FrameID
	refs       map[string]refInfo
	refNode    map[cdp.BackendNodeID]string
	console    *ring[ConsoleEntry]
	reqs       map[network.RequestID]*Request
	reqOrder   *ring[network.RequestID]
	errTotal   int
	failTotal  int
	errMark    int
	failMark   int
	lastNet    time.Time
	mainStatus int
	lastURL    string
	mainDocID  network.RequestID
	frameSeen  map[string]bool
}

func freshTabState() tabState {
	return tabState{
		console: newRing[ConsoleEntry](ringSize), reqs: map[network.RequestID]*Request{},
		reqOrder: newRing[network.RequestID](ringSize), frameSeen: map[string]bool{},
	}
}

// TabInfo describes one of werkz's tabs.
type TabInfo struct {
	ID         string // stable for the life of the tab; pass it to SwitchTab and CloseTab
	Title, URL string
	Active     bool
}

// stashLocked saves the active tab's page state into t.
func (b *Browser) stashLocked(t *tab) {
	t.st = tabState{
		mainID: b.mainID, refs: b.refs, refNode: b.refNode, console: b.console, reqs: b.reqs, reqOrder: b.reqOrder,
		errTotal: b.errTotal, failTotal: b.failTotal, errMark: b.errMark, failMark: b.failMark, lastNet: b.lastNet,
		mainStatus: b.mainStatus, lastURL: b.lastURL, mainDocID: b.mainDocID, frameSeen: b.frameSeen,
	}
}

// activateLocked makes t the active tab: console, network, refs, the
// navigation policy's frame bookkeeping and the guard all follow it.
func (b *Browser) activateLocked(t *tab) {
	if b.cur == t {
		return
	}
	if b.cur != nil {
		b.stashLocked(b.cur)
	}
	s := t.st
	if s.console == nil {
		s = freshTabState()
	}
	b.mainID, b.refs, b.refNode, b.console, b.reqs, b.reqOrder = s.mainID, s.refs, s.refNode, s.console, s.reqs, s.reqOrder
	b.errTotal, b.failTotal, b.errMark, b.failMark, b.lastNet = s.errTotal, s.failTotal, s.errMark, s.failMark, s.lastNet
	b.mainStatus, b.lastURL, b.mainDocID, b.frameSeen = s.mainStatus, s.lastURL, s.mainDocID, s.frameSeen
	// events of a background tab were not tracked: nothing is known to be in flight
	b.reqStart, b.inflight, b.loading = map[network.RequestID]float64{}, 0, false
	b.tab, b.tabStop, b.cur = t.ctx, t.stop, t
}

// fallbackLocked moves the active tab to a live one when the user closed it.
func (b *Browser) fallbackLocked() {
	if b.cur == nil || !b.cur.gone.Load() {
		return
	}
	for i := len(b.tabs) - 1; i >= 0; i-- {
		if t := b.tabs[i]; !t.gone.Load() {
			b.activateLocked(t)
			return
		}
	}
}

func (b *Browser) listenTab(t *tab) error {
	return chromedp.Listen(t.ctx, func(ev any) { b.tabEvent(t, ev) })
}

// tabEvent routes a tab's events: every tab's paused requests are judged by
// the navigation policy (or the page would hang), the rest only count for the
// active tab.
func (b *Browser) tabEvent(t *tab, ev any) {
	if e, ok := ev.(*fetch.EventRequestPaused); ok {
		go b.onPaused(t, e)
		return
	}
	b.mu.Lock()
	active := b.cur == t
	b.mu.Unlock()
	if active {
		b.onEvent(ev)
	}
}

// tabGone notes that Chrome no longer has the tab (closed by the user or by us).
func (b *Browser) tabGone(id target.ID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, t := range b.tabs {
		if t.id == id {
			t.gone.Store(true)
		}
	}
}

func (b *Browser) findTabLocked(id string) *tab {
	for _, t := range b.tabs {
		if string(t.id) == id && !t.gone.Load() {
			return t
		}
	}
	return nil
}

// Tabs lists werkz's own tabs. Other tabs of the browser are never listed.
func (b *Browser) Tabs(call context.Context) ([]TabInfo, error) {
	b.opMu.Lock()
	defer b.opMu.Unlock()
	b.mu.Lock()
	if b.closed || b.root == nil {
		b.mu.Unlock()
		return nil, errors.New("the browser is closed")
	}
	b.fallbackLocked()
	tabs, cur := append([]*tab(nil), b.tabs...), b.cur
	b.mu.Unlock()
	var out []TabInfo
	for _, t := range tabs {
		if t.gone.Load() {
			continue
		}
		info := TabInfo{ID: string(t.id), Active: t == cur}
		ctx, cancel := context.WithTimeout(t.ctx, 3*time.Second)
		stop := context.AfterFunc(call, cancel)
		if loc, err := chromedp.Run(ctx, chromedp.Location()); err == nil {
			info.URL = loc
		}
		if ti, err := chromedp.Run(ctx, chromedp.Title()); err == nil {
			info.Title = ti
		}
		stop()
		cancel()
		out = append(out, info)
	}
	return out, nil
}

// NewTab opens a tab (url "" = about:blank), makes it the active one and
// navigates it like Navigate does: the caller has already approved the URL.
func (b *Browser) NewTab(call context.Context, rawURL string) (*Snapshot, error) {
	b.opMu.Lock()
	defer b.opMu.Unlock()
	return b.newTabLocked(call, rawURL)
}

func (b *Browser) newTabLocked(call context.Context, rawURL string) (*Snapshot, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "about:blank" {
		rawURL = ""
	}
	if rawURL != "" {
		if _, err := parseTarget(rawURL); err != nil {
			return nil, err
		}
	}
	b.mu.Lock()
	root, g, closed, n := b.root, b.g, b.closed, len(b.tabs)
	b.mu.Unlock()
	if closed || root == nil || g == nil {
		return nil, errors.New("the browser is closed")
	}
	if n >= maxTabs {
		return nil, fmt.Errorf("werkz already has %d tabs open: close one first", n)
	}
	// Hang the tab off the first tab's context for its browser connection,
	// but not for its lifetime: closing the first tab must not close the rest.
	tctx, tstop := chromedp.NewContext(context.WithoutCancel(root.ctx))
	if !b.connected {
		g.expectTab()
		defer g.endExpect()
	}
	started := make(chan error, 1)
	go func() { started <- chromedp.Do(tctx) }() // creates and attaches the tab
	select {
	case err := <-started:
		if err != nil {
			tstop()
			return nil, fmt.Errorf("could not open a tab: %w", err)
		}
	case <-call.Done():
		tstop()
		return nil, call.Err()
	case <-time.After(15 * time.Second):
		tstop()
		return nil, errors.New("the browser did not open a tab within 15s")
	}
	c := chromedp.FromContext(tctx)
	if c == nil || c.Target == nil {
		tstop()
		return nil, errors.New("the new tab is not attached")
	}
	t := &tab{id: c.Target.TargetID, ctx: tctx, stop: tstop, st: freshTabState()}
	g.watchTab(t.id, b.connected)
	if err := b.listenTab(t); err != nil {
		tstop()
		return nil, err
	}
	octx, ocancel := b.opContext(call, tctx)
	defer ocancel()
	if err := b.prepTab(octx); err != nil {
		tstop()
		return nil, err
	}
	if err := b.loadMainID(octx, t); err != nil {
		tstop()
		return nil, err
	}
	b.mu.Lock()
	b.tabs = append(b.tabs, t)
	b.activateLocked(t)
	b.mu.Unlock()
	if rawURL != "" {
		return b.navigateLocked(call, rawURL)
	}
	b.settle(octx, 2*time.Second)
	return b.snapshot(octx)
}

// SwitchTab makes the tab with that ID the active one.
func (b *Browser) SwitchTab(call context.Context, id string) (*Snapshot, error) {
	b.opMu.Lock()
	defer b.opMu.Unlock()
	b.mu.Lock()
	t := b.findTabLocked(id)
	closed := b.closed
	if t != nil {
		b.activateLocked(t)
	}
	b.mu.Unlock()
	if closed {
		return nil, errors.New("the browser is closed")
	}
	if t == nil {
		return nil, fmt.Errorf("no tab %q: list the tabs first", id)
	}
	ctx, cancel := b.opContext(call, t.ctx)
	defer cancel()
	if loc, err := chromedp.Run(ctx, chromedp.Location()); err == nil {
		b.mu.Lock()
		b.lastURL = loc
		b.mu.Unlock()
	}
	b.settle(ctx, 3*time.Second)
	return b.snapshot(ctx)
}

// CloseTab closes one of werkz's tabs. Closing the last one opens a blank tab
// first, so werkz always has a tab.
func (b *Browser) CloseTab(call context.Context, id string) error {
	b.opMu.Lock()
	defer b.opMu.Unlock()
	b.mu.Lock()
	t := b.findTabLocked(id)
	root, closed, live := b.root, b.closed, 0
	for _, x := range b.tabs {
		if !x.gone.Load() {
			live++
		}
	}
	b.mu.Unlock()
	if closed || root == nil {
		return errors.New("the browser is closed")
	}
	if t == nil {
		return fmt.Errorf("no tab %q: list the tabs first", id)
	}
	if live <= 1 {
		if _, err := b.newTabLocked(call, ""); err != nil {
			return fmt.Errorf("could not open a replacement tab: %w", err)
		}
	}
	b.mu.Lock()
	t.gone.Store(true)
	for i, x := range b.tabs {
		if x == t {
			b.tabs = append(b.tabs[:i:i], b.tabs[i+1:]...)
			break
		}
	}
	if b.cur == t {
		b.cur = nil // its state is dropped with it
		b.tab, b.tabStop = nil, nil
		for i := len(b.tabs) - 1; i >= 0; i-- {
			if !b.tabs[i].gone.Load() {
				b.activateLocked(b.tabs[i])
				break
			}
		}
	}
	b.mu.Unlock()

	cctx, cancel := context.WithTimeout(root.ctx, 5*time.Second)
	defer cancel()
	_, err := chromedp.CallBrowser(cctx, target.CloseTarget, target.CloseTargetParams{TargetID: t.id})
	if !t.root { // the first tab's context stays: it carries the browser connection
		t.stop()
	}
	if err != nil && !strings.Contains(err.Error(), "No target") {
		return fmt.Errorf("could not close the tab: %w", err)
	}
	return nil
}
