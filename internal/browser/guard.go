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
	conn conn
	next atomic.Int64
	done chan struct{}

	mu        sync.Mutex
	pending   map[int64]chan *cdproto.Message
	sessions  map[target.SessionID]*gsession
	closed    bool
	tabs      map[target.ID]bool // werkz's own tabs (everything else that appears is a popup)
	expecting bool               // a tab werkz is creating right now (launch mode) is about to appear
	tabReady  chan struct{}      // closed once werkz's tab is watched for iframes
	tabOnce   sync.Once
	byTarget  map[target.ID]*gsession // connected mode: popups of werkz's tab
}

type gsession struct {
	targetID target.ID
	typ      string
	popup    bool
	decided  bool
	counted  bool // popupBegin was called for it
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
	return newGuard(b, conn, tabID), nil
}

func newGuard(b *Browser, c conn, tabID target.ID) *guard {
	g := &guard{
		b: b, conn: c, done: make(chan struct{}), tabs: map[target.ID]bool{tabID: true}, tabReady: make(chan struct{}),
		pending:  map[int64]chan *cdproto.Message{},
		sessions: map[target.SessionID]*gsession{},
		byTarget: map[target.ID]*gsession{},
	}
	go g.readLoop()
	return g
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

func (g *guard) isTab(id target.ID) bool {
	g.mu.Lock()
	own := g.tabs[id]
	g.mu.Unlock()
	// connected mode: the fence knows a tab the moment its creation is answered,
	// before the browser has registered it here
	return own || (g.b.connected && g.b.pol != nil && g.b.pol.isTab(id))
}

// expectTab says that werkz is about to create a tab (launch mode, where every
// new page is paused for vetting): the first new page without an opener is it.
func (g *guard) expectTab() {
	g.mu.Lock()
	g.expecting = true
	g.mu.Unlock()
}

func (g *guard) endExpect() {
	g.mu.Lock()
	g.expecting = false
	g.mu.Unlock()
}

func (g *guard) takeExpected(id target.ID) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.expecting {
		return false
	}
	g.expecting = false
	g.tabs[id] = true
	return true
}

// watchTab registers a tab werkz created. In connected mode the guard attaches
// to it to watch for out-of-process iframes.
func (g *guard) watchTab(id target.ID, connected bool) {
	g.mu.Lock()
	g.tabs[id] = true
	g.mu.Unlock()
	if connected {
		_, _ = gcall(g, "", target.AttachToTarget, target.AttachToTargetParams{TargetID: id, Flatten: new(true)})
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

// startConnected watches werkz's tab in the user's own Chrome. Chrome cannot
// pause a popup for us there (that would need browser-wide auto-attach, which
// pauses the user's popups too), so it asks to hear about new pages; the
// fence lets through only the ones werkz's own tab opened.
func (g *guard) startConnected() error {
	if _, err := gcall(g, "", target.SetDiscoverTargets, target.SetDiscoverTargetsParams{Discover: true, Filter: target.Filter{{Type: "page"}}}); err != nil {
		return err
	}
	g.mu.Lock()
	var id target.ID
	for t := range g.tabs {
		id = t
	}
	g.mu.Unlock()
	_, err := gcall(g, "", target.AttachToTarget, target.AttachToTargetParams{TargetID: id, Flatten: new(true)})
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
		gone := g.sessions[e.SessionID]
		delete(g.sessions, e.SessionID)
		g.mu.Unlock()
		if gone != nil && g.isTab(gone.targetID) {
			g.b.tabGone(gone.targetID)
		}
	case *target.EventTargetCreated:
		g.discovered(e.TargetInfo)
	case *target.EventTargetInfoChanged:
		g.discovered(e.TargetInfo)
	case *target.EventTargetDestroyed:
		if g.isTab(e.TargetID) {
			g.b.tabGone(e.TargetID)
		}
		g.destroyed(e.TargetID)
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
	if known := g.byTarget[ti.TargetID]; known != nil {
		s = known // a popup found by discovery
	}
	g.sessions[e.SessionID] = s
	g.mu.Unlock()

	switch {
	case ti.Type == "page" && (g.isTab(ti.TargetID) || (e.WaitingForDebugger && ti.OpenerID == "" && g.takeExpected(ti.TargetID))):
		// werkz's own tab: watch for out-of-process iframes.
		_, _ = gcall(g, e.SessionID, target.SetAutoAttach, autoAttachParams())
		g.tabOnce.Do(func() { close(g.tabReady) })
		if e.WaitingForDebugger {
			g.resume(e.SessionID)
		}
	case ti.Type == "page" && (e.WaitingForDebugger || s.popup):
		g.mu.Lock()
		s.popup = true
		counted := s.counted
		s.counted = true
		g.mu.Unlock()
		if !counted {
			g.b.popupBegin()
		}
		_, err := gcall(g, e.SessionID, fetch.Enable, fetch.EnableParams{
			Patterns: []*fetch.RequestPattern{{ResourceType: network.ResourceTypeDocument, RequestStage: fetch.RequestStageRequest}},
		})
		if err != nil {
			g.endPopup(e.SessionID, s, "")
			return
		}
		g.resume(e.SessionID)
		if !counted {
			time.AfterFunc(popupWait, func() { g.endPopup(e.SessionID, s, "") })
		}
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

// claim reports whether the caller is the one to decide popup s.
func (g *guard) claim(s *gsession) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s.decided {
		return false
	}
	s.decided = true
	return true
}

// endPopup closes a popup (once) and, if navURL is set, sends werkz's tab
// there instead.
func (g *guard) endPopup(sess target.SessionID, s *gsession, navURL string) {
	if g.claim(s) {
		g.finishPopup(s, navURL)
	}
}

func (g *guard) finishPopup(s *gsession, navURL string) {
	_, _ = gcall(g, "", target.CloseTarget, target.CloseTargetParams{TargetID: s.targetID})
	g.mu.Lock()
	delete(g.byTarget, s.targetID)
	g.mu.Unlock()
	if navURL != "" {
		g.b.navigateTab(navURL)
	}
	g.b.popupEnd()
}

// discovered handles a page the fence let through: a popup that werkz's tab
// opened. It is attached for vetting, and decided as soon as its address is
// known, or closed when it stays blank.
func (g *guard) discovered(ti *target.Info) {
	if ti == nil || g.isTab(ti.TargetID) || ti.Type != "page" {
		return
	}
	g.mu.Lock()
	s := g.byTarget[ti.TargetID]
	fresh := s == nil
	if fresh {
		s = &gsession{targetID: ti.TargetID, typ: ti.Type, popup: true, counted: true}
		g.byTarget[ti.TargetID] = s
	}
	g.mu.Unlock()
	if fresh {
		g.b.popupBegin()
		time.AfterFunc(popupWait, func() { g.endPopup("", s, "") })
		go func() {
			_, _ = gcall(g, "", target.AttachToTarget, target.AttachToTargetParams{TargetID: ti.TargetID, Flatten: new(true)})
		}()
	}
	if u := ti.URL; u != "" && u != "about:blank" && g.claim(s) {
		navURL := ""
		if g.b.navAllowed(u) {
			navURL = u
		} else {
			g.b.addBlocked("popup to " + truncate(u, 200))
		}
		g.finishPopup(s, navURL)
	}
}

// destroyed notes a popup that closed itself.
func (g *guard) destroyed(id target.ID) {
	g.mu.Lock()
	s := g.byTarget[id]
	delete(g.byTarget, id)
	g.mu.Unlock()
	if s != nil && g.claim(s) {
		g.b.popupEnd()
	}
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

// The page guard is injected into every document. It keeps new windows in
// this tab (so the navigation policy judges them) and, while Eval runs, locks
// secret fields. It holds no switch a page could flip: whether a form may be
// submitted or a file downloaded is decided on the protocol side, in Go (the
// Fetch policy for POST documents, Page.downloadWillBegin for downloads).
const submitGuardJS = `(function(K){
 if(Object.prototype.hasOwnProperty.call(window,K))return;
 Object.defineProperty(window,K,{value:true,enumerable:false});
 var P=HTMLFormElement.prototype,os=P.submit;
 Object.defineProperty(P,'submit',{value:function(){fixT(this);return os.apply(this,arguments)},writable:true,configurable:true,enumerable:true});
 window.addEventListener('submit',function(e){fixT(e.target)},true);
 /* New windows: every route that would open one (target=_blank, formtarget,
    window.open, ctrl/middle click) navigates this tab instead, so the
    navigation policy judges the address before any request is made. */
 var frameNamed=function(n){try{if(!n)return false;var l=document.getElementsByName(n);for(var i=0;i<l.length;i++){if(l[i].tagName==='IFRAME'||l[i].tagName==='FRAME')return true}}catch(e){}return false};
 var selfish=function(t){t=String(t||'').toLowerCase();return t===''||t==='_self'||t==='_parent'||t==='_top'};
 var fixT=function(el){try{var t=el.getAttribute('target');if(t===null){var b=document.querySelector('base[target]');t=b?b.getAttribute('target'):''}
  if(!selfish(t)&&!frameNamed(t))el.setAttribute('target','_self')}catch(e){}};
 var go=function(h){try{var u=new URL(String(h),document.baseURI);if(u.protocol==='http:'||u.protocol==='https:')location.assign(u.href)}catch(e){}};
 var oo=window.open;
 /* the real window.open stays reachable under a private name: a popup made that way is still vetted by the popup watcher */
 if(oo)Object.defineProperty(window,K+'o',{value:oo,enumerable:false});
 Object.defineProperty(window,'open',{value:function(u,n){try{if(n&&frameNamed(n)&&oo)return oo.apply(window,arguments);
  if(u!==undefined&&u!==null&&String(u)!=='')go(u)}catch(e){}return null},writable:true,configurable:true,enumerable:true});
 var path=function(e){return (e.composedPath&&e.composedPath())||[]};
 var link=function(e){var p=path(e),a=null,f=null,fm=null;
  for(var i=0;i<p.length;i++){var n=p[i];if(!n||n.nodeType!==1)continue;
   if(!a&&n.matches('a[href],area[href]'))a=n;
   if(!f&&n.matches('button[formtarget],input[formtarget]'))f=n;
   if(!fm&&n.tagName==='FORM')fm=n}
  if(a){fixT(a);if(!a.hasAttribute('download')&&(e.ctrlKey||e.metaKey||e.shiftKey||e.button===1)){e.preventDefault();go(a.href)}}
  if(f){var ft=f.getAttribute('formtarget');if(!selfish(ft)&&!frameNamed(ft))f.setAttribute('formtarget','_self')}
  if(fm)fixT(fm)};
 window.addEventListener('click',link,true);window.addEventListener('auxclick',link,true);
 /* Eval: while a script runs for the model, password and payment fields cannot be read or written. This only
    stops accidental reads through the value accessors; it is not a sandbox (see Browser.Eval). */
 var evalOn=false,MSG='werkz never reads or sets password or payment fields';
 var secretRe=/card[-_ ]?(number|num|no)\b|\bcvc\b|\bcvv\b|cc[-_]?(num|number|csc|exp)/i;
 var isSecret=function(el){try{var tg=String(el.tagName||'').toLowerCase();if(tg!=='input'&&tg!=='textarea')return false;
  if(String(el.type||'').toLowerCase()==='password')return true;
  var ac=String(el.getAttribute('autocomplete')||'').toLowerCase().split(/\s+/);
  for(var i=0;i<ac.length;i++){if(/^(current-password|new-password|one-time-code|cc-number|cc-csc|cc-exp|cc-exp-month|cc-exp-year)$/.test(ac[i]))return true}
  return secretRe.test(el.getAttribute('name')||'')||secretRe.test(el.id||'')}catch(e){return false}};
 var lock=function(proto,prop){var d=Object.getOwnPropertyDescriptor(proto,prop);if(!d||!d.get)return;
  Object.defineProperty(proto,prop,{configurable:true,enumerable:d.enumerable,
   get:function(){if(evalOn&&isSecret(this))throw new Error(MSG);return d.get.call(this)},
   set:d.set?function(v){if(evalOn&&isSecret(this))throw new Error(MSG);d.set.call(this,v)}:undefined})};
 ['value','defaultValue','valueAsNumber'].forEach(function(p){lock(HTMLInputElement.prototype,p)});
 ['value','defaultValue'].forEach(function(p){lock(HTMLTextAreaElement.prototype,p)});
 Object.defineProperty(window,K+'e',{value:function(v){evalOn=(v===true);
  for(var i=0;i<window.frames.length;i++){try{var f=window.frames[i][K+'e'];if(typeof f==='function')f(v)}catch(e){}}},enumerable:false});
})(%q)`

const evalSwitchJS = `(function(K){var f=window[K+'e'];if(typeof f==='function')f(%s)})(%q)`

func (b *Browser) guardScript() string { return fmt.Sprintf(submitGuardJS, b.guardKey) }

// installSubmitGuard injects the script into future documents and the current
// one, and starts listening for blocked submissions.
func (b *Browser) installSubmitGuard(ctx context.Context) error {
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

// arm lets form submissions through until the returned func is called. The
// switch is Go's alone: the network layer refuses a POST document otherwise,
// and nothing in the page can reach it.
func (b *Browser) arm(context.Context) func() {
	b.mu.Lock()
	b.armed = true
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		b.armed = false
		b.mu.Unlock()
	}
}
