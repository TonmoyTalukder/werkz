package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/cdp/jsonv2"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/remote"
)

// guard is a second, raw DevTools connection to the same browser. chromedp
// routes messages by the sessions it created itself, so it cannot answer the
// sessions of targets Chrome attaches on its own (popups, out-of-process
// iframes, workers). Those must be paused before they load, vetted, and then
// resumed or closed, so the guard speaks the protocol for them directly.
type guard struct {
	b    *Browser
	conn *remote.Conn
	next atomic.Int64
	done chan struct{}

	mu       sync.Mutex
	pending  map[int64]chan *cdproto.Message
	sessions map[target.SessionID]*gsession
	closed   bool
	tabID    target.ID
	tabReady chan struct{} // closed once werkz's tab is watched for iframes
	tabOnce  sync.Once
}

type gsession struct {
	targetID target.ID
	typ      string
	popup    bool
	decided  bool
}

const popupWait = 2 * time.Second // how long a popup may stay silent before it is closed

// readActivePort returns the browser websocket URL that Chrome wrote into
// the profile directory.
func readActivePort(dir string) (string, error) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(filepath.Join(dir, "DevToolsActivePort"))
		if err == nil {
			f := strings.Fields(string(data))
			if len(f) >= 2 {
				return "ws://127.0.0.1:" + f[0] + f[1], nil
			}
		}
		if time.Now().After(deadline) {
			return "", errors.New("the browser did not report its debugging address")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func dialGuard(ctx context.Context, b *Browser, wsURL string, tabID target.ID) (*guard, error) {
	conn, err := remote.DialContext(ctx, wsURL)
	if err != nil {
		return nil, err
	}
	g := &guard{
		b: b, conn: conn, done: make(chan struct{}), tabID: tabID, tabReady: make(chan struct{}),
		pending:  map[int64]chan *cdproto.Message{},
		sessions: map[target.SessionID]*gsession{},
	}
	go g.readLoop()
	return g, nil
}

func (g *guard) readLoop() {
	defer close(g.done)
	for {
		msg := new(cdproto.Message)
		if err := g.conn.Read(context.Background(), msg); err != nil {
			g.mu.Lock()
			g.closed = true
			for id, ch := range g.pending {
				close(ch)
				delete(g.pending, id)
			}
			g.mu.Unlock()
			return
		}
		if msg.ID != 0 {
			g.mu.Lock()
			ch := g.pending[msg.ID]
			delete(g.pending, msg.ID)
			g.mu.Unlock()
			if ch != nil {
				ch <- msg
			}
			continue
		}
		if msg.Method == "" {
			continue
		}
		ev, err := cdproto.UnmarshalMessage(msg, chromedp.DefaultUnmarshalOptions)
		if err != nil {
			continue
		}
		go g.onEvent(msg.SessionID, ev)
	}
}

func (g *guard) alive() bool {
	select {
	case <-g.done:
		return false
	default:
		return true
	}
}

func (g *guard) close() {
	_ = g.conn.Close()
}

// send runs a command on a session ("" = the browser itself).
func (g *guard) send(ctx context.Context, sess target.SessionID, method string, params, res any) error {
	id := g.next.Add(1)
	ch := make(chan *cdproto.Message, 1)
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return errors.New("guard connection closed")
	}
	g.pending[id] = ch
	g.mu.Unlock()
	msg := &cdproto.Message{ID: id, SessionID: sess, Method: cdproto.MethodType(method)}
	if params != nil {
		raw, err := jsonv2.Marshal(params, chromedp.DefaultMarshalOptions)
		if err != nil {
			return err
		}
		msg.Params = raw
	}
	if err := g.conn.Write(ctx, msg); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		g.mu.Lock()
		delete(g.pending, id)
		g.mu.Unlock()
		return ctx.Err()
	case m, ok := <-ch:
		if !ok {
			return errors.New("guard connection closed")
		}
		if m.Error != nil {
			return m.Error
		}
		if res != nil {
			return jsonv2.Unmarshal(m.Result, res, chromedp.DefaultUnmarshalOptions)
		}
		return nil
	}
}

func gcall[P, R any](g *guard, sess target.SessionID, cmd cdp.Command[P, R], p P) (R, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var r R
	err := g.send(ctx, sess, cmd.Method, p, &r)
	return r, err
}

func autoAttachParams() target.SetAutoAttachParams {
	return target.SetAutoAttachParams{AutoAttach: true, WaitForDebuggerOnStart: true, Flatten: new(true)}
}

// start turns on auto-attach at the browser level: every new page (popup)
// pauses before it loads.
func (g *guard) start() error {
	_, err := gcall(g, "", target.SetAutoAttach, autoAttachParams())
	return err
}

func (g *guard) resume(sess target.SessionID) {
	_, _ = gcall(g, sess, runtime.RunIfWaitingForDebugger, cdp.Empty{})
}

func (g *guard) onEvent(sess target.SessionID, ev any) {
	switch e := ev.(type) {
	case *target.EventAttachedToTarget:
		g.attached(e)
	case *target.EventDetachedFromTarget:
		g.mu.Lock()
		delete(g.sessions, e.SessionID)
		g.mu.Unlock()
	case *fetch.EventRequestPaused:
		g.paused(sess, e)
	case *network.EventRequestWillBeSent, *network.EventResponseReceived, *network.EventLoadingFailed, *network.EventLoadingFinished:
		// An out-of-process iframe reports its own loading here; without it
		// the request would look in flight forever.
		g.b.onEvent(ev)
	}
}

func (g *guard) attached(e *target.EventAttachedToTarget) {
	ti := e.TargetInfo
	if ti == nil {
		if e.WaitingForDebugger {
			g.resume(e.SessionID)
		}
		return
	}
	s := &gsession{targetID: ti.TargetID, typ: ti.Type}
	g.mu.Lock()
	g.sessions[e.SessionID] = s
	g.mu.Unlock()

	switch {
	case ti.Type == "page" && ti.TargetID == g.tabID:
		// werkz's own tab: watch for out-of-process iframes.
		_, _ = gcall(g, e.SessionID, target.SetAutoAttach, autoAttachParams())
		g.tabOnce.Do(func() { close(g.tabReady) })
		if e.WaitingForDebugger {
			g.resume(e.SessionID)
		}
	case ti.Type == "page" && e.WaitingForDebugger:
		s.popup = true
		g.b.popupBegin()
		_, err := gcall(g, e.SessionID, fetch.Enable, fetch.EnableParams{
			Patterns: []*fetch.RequestPattern{{ResourceType: network.ResourceTypeDocument, RequestStage: fetch.RequestStageRequest}},
		})
		if err != nil {
			g.endPopup(e.SessionID, s, "")
			return
		}
		g.resume(e.SessionID)
		time.AfterFunc(popupWait, func() { g.endPopup(e.SessionID, s, "") })
	case ti.Type == "iframe":
		_, _ = gcall(g, e.SessionID, network.Enable, network.EnableParams{})
		if _, err := gcall(g, e.SessionID, fetch.Enable, fetch.EnableParams{
			Patterns: []*fetch.RequestPattern{{ResourceType: network.ResourceTypeDocument, RequestStage: fetch.RequestStageRequest}},
		}); err == nil {
			_, _ = gcall(g, e.SessionID, target.SetAutoAttach, autoAttachParams())
		}
		if e.WaitingForDebugger {
			g.resume(e.SessionID)
		}
	default:
		// workers, service workers, other pages that were already open
		if e.WaitingForDebugger {
			g.resume(e.SessionID)
		}
	}
}

// endPopup closes a popup (once) and, if navURL is set, sends werkz's tab
// there instead.
func (g *guard) endPopup(sess target.SessionID, s *gsession, navURL string) {
	g.mu.Lock()
	if s.decided {
		g.mu.Unlock()
		return
	}
	s.decided = true
	g.mu.Unlock()
	_, _ = gcall(g, "", target.CloseTarget, target.CloseTargetParams{TargetID: s.targetID})
	if navURL != "" {
		g.b.navigateTab(navURL)
	}
	g.b.popupEnd()
}

func (g *guard) paused(sess target.SessionID, e *fetch.EventRequestPaused) {
	g.mu.Lock()
	s := g.sessions[sess]
	g.mu.Unlock()
	if s == nil || e.Request == nil {
		_, _ = gcall(g, sess, fetch.ContinueRequest, fetch.ContinueRequestParams{RequestID: e.RequestID})
		return
	}
	raw, method := e.Request.URL, e.Request.Method
	if s.popup {
		navURL := ""
		if method == "POST" || !g.b.navAllowed(raw) {
			g.b.addBlocked("popup to " + truncate(raw, 200))
		} else {
			navURL = raw
		}
		_, _ = gcall(g, sess, fetch.FailRequest, fetch.FailRequestParams{RequestID: e.RequestID, ErrorReason: network.ErrorReasonAborted})
		g.endPopup(sess, s, navURL)
		return
	}
	// an iframe's own document
	if g.b.frameAllowed(raw, method) {
		_, _ = gcall(g, sess, fetch.ContinueRequest, fetch.ContinueRequestParams{RequestID: e.RequestID})
		return
	}
	_, _ = gcall(g, sess, fetch.FulfillRequest, fetch.FulfillRequestParams{RequestID: e.RequestID, ResponseCode: 204})
}

// navigateTab sends werkz's tab to raw (a popup or target=_blank link that
// passed the same checks as any main-frame navigation).
func (b *Browser) navigateTab(raw string) {
	b.mu.Lock()
	tab := b.tab
	b.mu.Unlock()
	if tab == nil {
		return
	}
	ctx, cancel := context.WithTimeout(tab, 10*time.Second)
	defer cancel()
	_, _ = chromedp.Call(ctx, page.Navigate, page.NavigateParams{URL: raw})
}

func (b *Browser) popupBegin() {
	b.mu.Lock()
	b.popups++
	b.mu.Unlock()
}

func (b *Browser) popupEnd() {
	b.mu.Lock()
	b.popups--
	b.lastNet = time.Now()
	b.mu.Unlock()
}

// frameAllowed vets one subframe document request. Denied hosts are recorded
// once each.
func (b *Browser) frameAllowed(raw, method string) bool {
	if method == "POST" && !b.isArmed() {
		b.addBlocked(submitNote(raw))
		return false
	}
	if b.navAllowed(raw) {
		return true
	}
	b.mu.Lock()
	h := hostOf(raw)
	seen := b.frameSeen[h]
	if !seen {
		b.frameSeen[h] = true
	}
	b.mu.Unlock()
	if !seen {
		b.addBlocked("frame from " + truncate(raw, 200))
	}
	return false
}

func submitNote(raw string) string {
	return "form submit to " + truncate(raw, 200) + " (needs the user's approval)"
}

// The submit guard is injected into every document. It stops form
// submissions from every route (user events, submit(), requestSubmit(),
// handlers that fire later) unless an action armed it.
const submitGuardJS = `(function(K){
 if(Object.prototype.hasOwnProperty.call(window,K))return;
 var armed=false;
 var rec=function(f){try{var a;try{a=new URL((f.getAttribute&&f.getAttribute('action'))||'',document.baseURI).href}catch(e){a=String(f.action)}
  if(typeof window.__werkzBlocked==='function')window.__werkzBlocked(a)}catch(e){}};
 var P=HTMLFormElement.prototype,os=P.submit,or=P.requestSubmit;
 Object.defineProperty(P,'submit',{value:function(){if(!armed){rec(this);return}return os.apply(this,arguments)},writable:true,configurable:true,enumerable:true});
 if(or)Object.defineProperty(P,'requestSubmit',{value:function(){if(!armed){rec(this);return}return or.apply(this,arguments)},writable:true,configurable:true,enumerable:true});
 window.addEventListener('submit',function(e){if(!armed){e.preventDefault();e.stopImmediatePropagation();rec(e.target)}},true);
 Object.defineProperty(window,K,{value:function(v){armed=(v===true)},enumerable:false});
})(%q)`

const armJS = `(function(K){if(typeof window[K]==='function')window[K](%s)})(%q)`

func (b *Browser) guardScript() string { return fmt.Sprintf(submitGuardJS, b.guardKey) }

// installSubmitGuard injects the script into future documents and the current
// one, and starts listening for blocked submissions.
func (b *Browser) installSubmitGuard(ctx context.Context) error {
	if _, err := chromedp.Call(ctx, runtime.AddBinding, runtime.AddBindingParams{Name: "__werkzBlocked"}); err != nil {
		return err
	}
	src := b.guardScript()
	if _, err := chromedp.Call(ctx, page.AddScriptToEvaluateOnNewDocument, page.AddScriptToEvaluateOnNewDocumentParams{Source: src}); err != nil {
		return err
	}
	_, err := chromedp.Call(ctx, runtime.Evaluate, runtime.EvaluateParams{Expression: src})
	return err
}

func (b *Browser) isArmed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.armed
}

// arm lets form submissions through until the returned func is called. New
// documents created meanwhile (a submission's response page) start armed too.
func (b *Browser) arm(ctx context.Context) func() {
	b.mu.Lock()
	b.armed = true
	b.mu.Unlock()
	eval := func(v string) {
		_, _ = chromedp.Call(ctx, runtime.Evaluate, runtime.EvaluateParams{Expression: fmt.Sprintf(armJS, v, b.guardKey)})
	}
	eval("true")
	id, err := chromedp.Call(ctx, page.AddScriptToEvaluateOnNewDocument, page.AddScriptToEvaluateOnNewDocumentParams{Source: fmt.Sprintf(armJS, "true", b.guardKey)})
	return func() {
		b.mu.Lock()
		b.armed = false
		tab := b.tab
		b.mu.Unlock()
		if tab == nil {
			return
		}
		dctx, cancel := context.WithTimeout(tab, 5*time.Second)
		defer cancel()
		_, _ = chromedp.Call(dctx, runtime.Evaluate, runtime.EvaluateParams{Expression: fmt.Sprintf(armJS, "false", b.guardKey)})
		if err == nil {
			_, _ = chromedp.Call(dctx, page.RemoveScriptToEvaluateOnNewDocument, page.RemoveScriptToEvaluateOnNewDocumentParams(id))
		}
	}
}
