package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// ConsoleEntry is one console API call, uncaught exception or browser log line.
type ConsoleEntry struct {
	Level, Text, Source string
	Line                int
	At                  time.Time
}

// Request is one network request the page made.
type Request struct {
	Method, URL, Type string
	Status            int
	Failed            string // net error text
	Duration          time.Duration
}

func (r Request) failed() bool { return r.Failed != "" || r.Status >= 400 }

// Console returns the captured console output, optionally only errors, and
// optionally empties the buffer.
func (b *Browser) Console(errorsOnly, clear bool) []ConsoleEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []ConsoleEntry
	if b.console == nil {
		return nil
	}
	for _, e := range b.console.items() {
		if !errorsOnly || e.Level == "error" {
			out = append(out, e)
		}
	}
	if clear {
		b.console.clear()
	}
	return out
}

// Network returns the captured requests in order. Failed means a network
// error or a 4xx/5xx status. contains filters on the URL.
func (b *Browser) Network(failedOnly, clear bool, contains string) []Request {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []Request
	if b.reqOrder == nil {
		return nil
	}
	for _, id := range b.reqOrder.items() {
		r, ok := b.reqs[id]
		if !ok || (failedOnly && !r.failed()) || (contains != "" && !strings.Contains(r.URL, contains)) {
			continue
		}
		out = append(out, *r)
	}
	if clear {
		b.reqs = map[network.RequestID]*Request{}
		b.reqOrder.clear()
	}
	return out
}

// onEvent runs on the tab's message loop: it must be quick and must not send
// commands, so anything that needs one goes to a goroutine.
func (b *Browser) onEvent(ev any) {
	switch e := ev.(type) {
	case *runtime.EventConsoleAPICalled:
		b.addConsole(consoleLevel(string(e.Type)), argsText(e.Args), stackSource(e.StackTrace), stackLine(e.StackTrace), runtimeTime(e.Timestamp))
	case *runtime.EventExceptionThrown:
		d := e.ExceptionDetails
		if d == nil {
			return
		}
		text := d.Text
		if d.Exception != nil && d.Exception.Description != "" {
			text = d.Exception.Description
		}
		src := d.URL
		if src == "" {
			src = stackSource(d.StackTrace)
		}
		b.addConsole("error", text, src, int(d.LineNumber)+1, runtimeTime(e.Timestamp))
	case *log.EventEntryAdded:
		if e.Entry == nil {
			return
		}
		lvl := string(e.Entry.Level)
		if lvl == "verbose" {
			lvl = "debug"
		}
		b.addConsole(lvl, e.Entry.Text, e.Entry.URL, int(e.Entry.LineNumber)+1, runtimeTime(e.Entry.Timestamp))
	case *network.EventRequestWillBeSent:
		b.onRequestSent(e)
	case *network.EventResponseReceived:
		b.onResponse(e)
	case *network.EventLoadingFailed:
		b.onLoadingFailed(e)
	case *network.EventLoadingFinished:
		b.mu.Lock()
		if r, ok := b.reqs[e.RequestID]; ok {
			if st, live := b.reqStart[e.RequestID]; live {
				r.Duration = time.Duration((e.Timestamp.Float64() - st) * float64(time.Second))
			}
		}
		b.endRequestLocked(e.RequestID)
		b.mu.Unlock()
	case *fetch.EventRequestPaused:
		go b.onPaused(e)
	case *page.EventFrameStartedLoading:
		b.mu.Lock()
		if e.FrameID == b.mainID {
			b.loading = true
		}
		b.mu.Unlock()
	case *page.EventFrameStoppedLoading:
		b.mu.Lock()
		if e.FrameID == b.mainID {
			b.loading = false
		}
		b.mu.Unlock()
	case *page.EventFrameNavigated:
		b.mu.Lock()
		if e.Frame != nil && e.Frame.ID == b.mainID {
			b.lastURL = e.Frame.URL + e.Frame.URLFragment
		}
		b.mu.Unlock()
	case *page.EventNavigatedWithinDocument:
		b.mu.Lock()
		if e.FrameID == b.mainID {
			b.lastURL = e.URL
		}
		b.mu.Unlock()
	case *runtime.EventBindingCalled:
		if e.Name == "__werkzBlocked" {
			b.addBlocked(submitNote(e.Payload))
		}
	case *browser.EventDownloadWillBegin:
		b.onDownloadBegin(e)
	case *browser.EventDownloadProgress:
		b.onDownloadProgress(e)
	}
}

func (b *Browser) addConsole(level, text, source string, line int, at time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.console != nil {
		b.console.add(ConsoleEntry{Level: level, Text: text, Source: source, Line: line, At: at})
	}
	if level == "error" {
		b.errTotal++
	}
}

func consoleLevel(t string) string {
	switch t {
	case "error", "assert":
		return "error"
	case "warning":
		return "warning"
	case "debug":
		return "debug"
	case "info":
		return "info"
	}
	return "log"
}

func runtimeTime(ts runtime.Timestamp) time.Time {
	return time.UnixMilli(int64(ts.Float64()))
}

func stackSource(st *runtime.StackTrace) string {
	if st != nil && len(st.CallFrames) > 0 {
		return st.CallFrames[0].URL
	}
	return ""
}

func stackLine(st *runtime.StackTrace) int {
	if st != nil && len(st.CallFrames) > 0 {
		return int(st.CallFrames[0].LineNumber) + 1
	}
	return 0
}

func argsText(args []*runtime.RemoteObject) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		if a == nil {
			continue
		}
		switch {
		case len(a.Value) > 0:
			var s string
			if json.Unmarshal(a.Value, &s) == nil {
				parts = append(parts, s)
			} else {
				parts = append(parts, string(a.Value))
			}
		case a.Description != "":
			parts = append(parts, a.Description)
		case a.UnserializableValue != "":
			parts = append(parts, string(a.UnserializableValue))
		default:
			parts = append(parts, string(a.Type))
		}
	}
	return strings.Join(parts, " ")
}

// quietType are long-lived requests that must not hold up "network idle".
func quietType(t network.ResourceType) bool {
	switch t {
	case network.ResourceTypeWebSocket, network.ResourceTypeEventSource, network.ResourceTypePing:
		return true
	}
	return false
}

func (b *Browser) onRequestSent(e *network.EventRequestWillBeSent) {
	if e.Request == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if e.Type == network.ResourceTypeDocument && e.FrameID == b.mainID {
		b.mainDocID = e.RequestID
	}
	if r, ok := b.reqs[e.RequestID]; ok { // redirect: same ID, new hop
		r.URL, r.Method = e.Request.URL, e.Request.Method
		return
	}
	b.reqs[e.RequestID] = &Request{Method: e.Request.Method, URL: e.Request.URL, Type: string(e.Type)}
	if old, evicted := b.reqOrder.add(e.RequestID); evicted {
		delete(b.reqs, old)
	}
	if !quietType(e.Type) {
		b.reqStart[e.RequestID] = e.Timestamp.Float64()
		b.inflight++
	}
	b.lastNet = time.Now()
}

func (b *Browser) onResponse(e *network.EventResponseReceived) {
	if e.Response == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if r, ok := b.reqs[e.RequestID]; ok {
		r.Status = int(e.Response.Status)
		if r.Status >= 400 {
			b.failTotal++
		}
	}
	if e.Type == network.ResourceTypeDocument && e.RequestID == b.mainDocID {
		b.mainStatus = int(e.Response.Status)
	}
}

func (b *Browser) onLoadingFailed(e *network.EventLoadingFailed) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if r, ok := b.reqs[e.RequestID]; ok && !e.Canceled {
		r.Failed = e.ErrorText
		b.failTotal++
	}
	if e.RequestID == b.mainDocID {
		b.loading = false // a failed document never reports "stopped loading"
	}
	b.endRequestLocked(e.RequestID)
}

func (b *Browser) endRequestLocked(id network.RequestID) {
	if _, live := b.reqStart[id]; live {
		delete(b.reqStart, id)
		b.inflight--
	}
	b.lastNet = time.Now()
}

// onPaused is the navigation policy. Every paused request is continued or
// answered, otherwise the page would hang.
func (b *Browser) onPaused(e *fetch.EventRequestPaused) {
	b.mu.Lock()
	tab, mainID := b.tab, b.mainID
	b.mu.Unlock()
	if tab == nil {
		return
	}
	allow := true
	if e.ResourceType == network.ResourceTypeDocument && e.Request != nil {
		if e.FrameID == mainID {
			allow = b.mainAllowed(e.Request.URL, e.Request.Method)
		} else {
			allow = b.frameAllowed(e.Request.URL, e.Request.Method)
		}
	}
	ctx, cancel := context.WithTimeout(tab, 10*time.Second)
	defer cancel()
	if allow {
		_, _ = chromedp.Call(ctx, fetch.ContinueRequest, fetch.ContinueRequestParams{RequestID: e.RequestID})
		return
	}
	// Failing the request would replace the page with Chrome's error page. A
	// 204 answer makes the browser treat the navigation as a no-op instead,
	// so the current page stays put.
	_, _ = chromedp.Call(ctx, fetch.FulfillRequest, fetch.FulfillRequestParams{RequestID: e.RequestID, ResponseCode: 204})
}

// mainAllowed decides one main-frame document request and records a block.
// A POST is a form submission: it needs an armed action, whatever the page
// script did to get there.
func (b *Browser) mainAllowed(raw, method string) bool {
	if method == "POST" && !b.isArmed() {
		b.addBlocked(submitNote(raw))
		return false
	}
	if !b.navAllowed(raw) {
		b.addBlocked(truncate(raw, 200))
		return false
	}
	// A history approval covers the first main-frame request only.
	if u, err := url.Parse(raw); err == nil {
		b.mu.Lock()
		if b.onceOnly && hostKey(u) == b.approved {
			b.approved, b.onceOnly = "", false
		}
		b.mu.Unlock()
	}
	return true
}

// addBlocked records something werkz stopped, for the next Snapshot.
func (b *Browser) addBlocked(note string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n := len(b.blocked); n > 0 && b.blocked[n-1] == note {
		return
	}
	b.blocked = append(b.blocked, note)
	if len(b.blocked) > maxBlocked {
		b.blocked = capTail(b.blocked, maxBlocked)
	}
}

// navAllowed decides whether a document may load at raw. Navigate() approves
// its target host:port (and so redirects within it) for the duration of the
// call, a history move approves its entry's host:port once; anything else
// goes through AllowHost. Hosts are compared as hostKey gives them.
func (b *Browser) navAllowed(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := hostKey(u)
	b.mu.Lock()
	approved := b.approved
	b.mu.Unlock()
	if approved != "" && host == approved {
		return true
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return b.opts.AllowHost == nil || b.opts.AllowHost(host)
}

func (b *Browser) onDownloadBegin(e *browser.EventDownloadWillBegin) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.dlSeen == nil || b.dlSeen[e.GUID] {
		return
	}
	b.dlSeen[e.GUID] = true
	b.dlOrder = append(b.dlOrder, e.GUID)
	if len(b.dlOrder) > maxDLSeen {
		for _, g := range b.dlOrder[:len(b.dlOrder)-maxDLSeen] {
			delete(b.dlSeen, g)
		}
		b.dlOrder = append([]string(nil), b.dlOrder[len(b.dlOrder)-maxDLSeen:]...)
	}
	name := e.SuggestedFilename
	if name == "" {
		name = filepath.Base(e.URL)
	}
	if b.dlDir == "" {
		b.downloads = capTail(append(b.downloads, name+" (blocked: downloads are off)"), maxBlocked)
		return
	}
	b.dlPending[e.GUID] = dlInfo{name: name, dir: b.dlDir}
}

func (b *Browser) onDownloadProgress(e *browser.EventDownloadProgress) {
	b.mu.Lock()
	defer b.mu.Unlock()
	info, ok := b.dlPending[e.GUID]
	if !ok {
		return
	}
	switch e.State {
	case browser.DownloadProgressStateCompleted:
		where := e.FilePath
		if where == "" {
			where = filepath.Join(info.dir, info.name)
		}
		b.downloads = capTail(append(b.downloads, fmt.Sprintf("%s (saved to %s)", info.name, where)), maxBlocked)
	case browser.DownloadProgressStateCanceled:
		b.downloads = capTail(append(b.downloads, info.name+" (canceled)"), maxBlocked)
	default:
		return
	}
	delete(b.dlPending, e.GUID)
}

func capTail(s []string, n int) []string {
	if len(s) > n {
		return append([]string(nil), s[len(s)-n:]...)
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
