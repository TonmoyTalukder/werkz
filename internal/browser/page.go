package browser

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// Snapshot is what every action returns so the model sees the effect.
type Snapshot struct {
	URL, Title string
	Status     int    // HTTP status of the main document (0 if unknown)
	Tree       string // accessibility tree text (see ReadPage), capped at 12,000 chars with a "… N more lines" note
	// Counts since the previous snapshot.
	NewConsoleErrors, NewFailedRequests int
	Blocked                             []string // main-frame navigations blocked by AllowHost since the last call
	Downloads                           []string // downloads blocked or saved since the last call (file name + status)
}

// Navigate opens url (or goes "back", "forward", "reload"). The caller has
// already approved the URL.
func (b *Browser) Navigate(ctx context.Context, rawURL string) (*Snapshot, error) {
	b.opMu.Lock()
	defer b.opMu.Unlock()
	return b.navigateLocked(ctx, rawURL)
}

// navigateLocked is Navigate with opMu held.
func (b *Browser) navigateLocked(call context.Context, rawURL string) (*Snapshot, error) {
	b.mu.Lock()
	tab, closed := b.tab, b.closed
	b.mu.Unlock()
	if closed || tab == nil {
		return nil, errors.New("the browser is closed")
	}
	ctx, cancel := b.opContext(call, tab)
	defer cancel()

	rawURL = strings.TrimSpace(rawURL)
	cmd := strings.ToLower(rawURL)
	b.mu.Lock()
	b.refs, b.refNode = nil, nil
	b.mainStatus = 0
	b.approved, b.approveAny = "", false
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.approved, b.approveAny = "", false
		b.mu.Unlock()
	}()

	switch cmd {
	case "back", "forward", "reload":
		b.mu.Lock()
		b.approveAny = true // history entries were approved when first opened
		b.mu.Unlock()
		if err := b.history(ctx, cmd); err != nil {
			return nil, err
		}
	default:
		u, err := parseTarget(rawURL)
		if err != nil {
			return nil, err
		}
		b.mu.Lock()
		b.approved = strings.ToLower(u.Host)
		b.mu.Unlock()
		res, err := chromedp.Call(ctx, page.Navigate, page.NavigateParams{URL: u.String()})
		if err != nil {
			return nil, fmt.Errorf("navigation failed: %w", err)
		}
		if res.ErrorText != "" {
			return nil, fmt.Errorf("could not open %s: %s", u, humanNetError(res.ErrorText))
		}
	}
	b.settle(ctx, 15*time.Second)
	return b.snapshot(ctx)
}

func (b *Browser) history(ctx context.Context, cmd string) error {
	if cmd == "reload" {
		_, err := chromedp.Call(ctx, page.Reload, page.ReloadParams{})
		return err
	}
	h, err := chromedp.Call(ctx, page.GetNavigationHistory, cdp.Empty{})
	if err != nil {
		return err
	}
	i := int(h.CurrentIndex)
	if cmd == "back" {
		i--
	} else {
		i++
	}
	if i < 0 || i >= len(h.Entries) {
		return fmt.Errorf("cannot go %s: no such history entry", cmd)
	}
	_, err = chromedp.Call(ctx, page.NavigateToHistoryEntry, page.NavigateToHistoryEntryParams{EntryID: h.Entries[i].ID})
	return err
}

// parseTarget accepts http(s) URLs, and bare "localhost:3000/x" forms.
func parseTarget(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, errors.New("url is empty")
	}
	if raw == "about:blank" {
		return &url.URL{Scheme: "about", Opaque: "blank"}, nil
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%q is not a valid URL", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("only http and https pages can be opened, not %q", u.Scheme)
	}
	return u, nil
}

func humanNetError(s string) string {
	switch {
	case strings.Contains(s, "CONNECTION_REFUSED"):
		return "connection refused (is the dev server running on that port?)"
	case strings.Contains(s, "NAME_NOT_RESOLVED"):
		return "host name not found"
	}
	return s
}

// settle waits for the page to calm down after an action: a navigation in
// flight finishes loading, then the network stays quiet for ~300ms. It gives
// up after max so a chatty page (polling, streaming) cannot hang the call.
func (b *Browser) settle(ctx context.Context, max time.Duration) {
	const quiet = 300 * time.Millisecond
	start := time.Now()
	b.mu.Lock()
	if b.lastNet.Before(start) {
		b.lastNet = start // the action itself counts as activity
	}
	b.mu.Unlock()
	for time.Since(start) < max {
		b.mu.Lock()
		busy := b.loading || b.inflight > 0
		since := time.Since(b.lastNet)
		b.mu.Unlock()
		if !busy && since >= quiet {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(40 * time.Millisecond):
		}
	}
}

// snapshot describes the page now and resets the "since last call" counters.
func (b *Browser) snapshot(ctx context.Context) (*Snapshot, error) {
	s := &Snapshot{}
	if loc, err := chromedp.Run(ctx, chromedp.Location()); err == nil {
		s.URL = loc
	}
	if t, err := chromedp.Run(ctx, chromedp.Title()); err == nil {
		s.Title = t
	}
	lines, err := b.treeLines(ctx, false, false)
	if err != nil {
		return nil, err
	}
	s.Tree = capLines(lines, maxTreeSnap)
	b.mu.Lock()
	s.Status = b.mainStatus
	s.NewConsoleErrors, s.NewFailedRequests = b.errTotal-b.errMark, b.failTotal-b.failMark
	b.errMark, b.failMark = b.errTotal, b.failTotal
	s.Blocked, s.Downloads = b.blocked, b.downloads
	b.blocked, b.downloads = nil, nil
	b.mu.Unlock()
	return s, nil
}

// capLines joins lines up to max characters and says how many were dropped.
func capLines(lines []string, max int) string {
	var sb strings.Builder
	for i, l := range lines {
		if sb.Len()+len(l)+1 > max {
			fmt.Fprintf(&sb, "… %d more lines", len(lines)-i)
			return sb.String()
		}
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	return strings.TrimRight(sb.String(), "\n")
}
