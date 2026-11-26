package browser

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto"
	"github.com/chromedp/cdproto/cdp/jsonv2"
	"github.com/chromedp/cdproto/target"
)

// The fence sits between werkz and the user's own Chrome. Chrome's debugging
// port gives a client the whole browser: every tab, its page and its cookies.
// werkz is only meant to use the tab it opened (and the popups that tab
// opened), so every message in both directions passes through here:
//
//   - outbound: commands that could reach anything else (other targets,
//     browser-wide settings such as downloads, the cookie jar, closing the
//     browser) are answered with an error and never reach Chrome;
//   - inbound: events about targets that are not werkz's are dropped before
//     any other code parses them, so the user's other tabs never show up in
//     werkz's state, logs or errors.
type conn interface {
	Read(context.Context, *cdproto.Message) error
	Write(context.Context, *cdproto.Message) error
	Close() error
}

// fencePolicy is the set of targets werkz may touch, shared by the fences of
// every connection to the same browser.
type fencePolicy struct {
	mu      sync.Mutex
	targets map[target.ID]bool // werkz's tabs and the popups they opened
	tabs    []target.ID        // the tabs werkz created, in order
	dead    map[target.ID]bool // tabs that were closed
	lost    chan struct{}      // closed when the last tab of werkz's is gone
	// download is asked about every download one of werkz's targets starts
	// (Page.downloadWillBegin); false makes the fence cancel it.
	download func(name, url string) bool
	lostOnce sync.Once
}

func newFencePolicy() *fencePolicy {
	return &fencePolicy{targets: map[target.ID]bool{}, lost: make(chan struct{})}
}

func (p *fencePolicy) has(id target.ID) bool {
	if id == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.targets[id]
}

func (p *fencePolicy) isTab(id target.ID) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, t := range p.tabs {
		if t == id {
			return true
		}
	}
	return false
}

func (p *fencePolicy) addTab(id target.ID) {
	p.mu.Lock()
	p.targets[id] = true
	p.tabs = append(p.tabs, id)
	p.mu.Unlock()
}

func (p *fencePolicy) addPopup(id target.ID) {
	p.mu.Lock()
	p.targets[id] = true
	p.mu.Unlock()
}

// dropTab notes that a tab is gone and returns how many werkz tabs remain. The
// tab stays known, so every connection's fence still passes its destroy event.
func (p *fencePolicy) dropTab(id target.ID) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dead == nil {
		p.dead = map[target.ID]bool{}
	}
	p.dead[id] = true
	n := 0
	for _, t := range p.tabs {
		if !p.dead[t] {
			n++
		}
	}
	return n
}

func (p *fencePolicy) remove(id target.ID) {
	p.mu.Lock()
	delete(p.targets, id)
	p.mu.Unlock()
}

func (p *fencePolicy) tabIDs() []target.ID {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []target.ID
	for _, t := range p.tabs {
		if !p.dead[t] {
			out = append(out, t)
		}
	}
	return out
}

func (p *fencePolicy) markLost() { p.lostOnce.Do(func() { close(p.lost) }) }

func (p *fencePolicy) isLost() bool {
	select {
	case <-p.lost:
		return true
	default:
		return false
	}
}

type fsession struct {
	root  target.ID // the werkz target this session belongs to
	child bool      // an auto-attached frame or worker of root
}

type fence struct {
	pol *fencePolicy
	c   conn

	in     chan *cdproto.Message // what the browser sent
	inject chan *cdproto.Message // answers the fence gives itself
	done   chan struct{}
	once   sync.Once
	rerr   error

	mu       sync.Mutex
	sessions map[target.SessionID]fsession
	pend     map[int64]string // commands whose answer teaches the fence something
	ownSeq   int64
}

func newFence(pol *fencePolicy, c conn) *fence {
	f := &fence{
		pol: pol, c: c,
		in: make(chan *cdproto.Message, 64), inject: make(chan *cdproto.Message, 256), done: make(chan struct{}),
		sessions: map[target.SessionID]fsession{}, pend: map[int64]string{},
	}
	go f.pump()
	return f
}

func (f *fence) pump() {
	defer close(f.in)
	for {
		m := new(cdproto.Message)
		if err := f.c.Read(context.Background(), m); err != nil {
			f.rerr = err
			return
		}
		select {
		case f.in <- m:
		case <-f.done:
			return
		}
	}
}

// Read returns the next message werkz may see.
func (f *fence) Read(ctx context.Context, msg *cdproto.Message) error {
	for {
		select {
		case m := <-f.inject:
			*msg = *m
			return nil
		case m, ok := <-f.in:
			if !ok {
				if f.rerr != nil {
					return f.rerr
				}
				return io.EOF
			}
			if f.filterIn(m) {
				*msg = *m
				return nil
			}
		case <-f.done:
			return io.EOF
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Write sends a command to the browser unless the fence refuses it.
func (f *fence) Write(ctx context.Context, msg *cdproto.Message) error {
	verdict, result, why := f.checkOut(msg)
	switch verdict {
	case fenceAllow:
		return f.c.Write(ctx, msg)
	case fenceSwallow:
		f.reply(&cdproto.Message{ID: msg.ID, SessionID: msg.SessionID, Result: jsonv2.Value(result)})
	default:
		f.reply(&cdproto.Message{ID: msg.ID, SessionID: msg.SessionID, Error: &cdproto.Error{Code: -32000, Message: "werkz is using your own Chrome and does not allow " + why}})
	}
	return nil
}

func (f *fence) reply(m *cdproto.Message) {
	select {
	case f.inject <- m:
	default:
		go func() {
			select {
			case f.inject <- m:
			case <-f.done:
			}
		}()
	}
}

// Close drops the connection. The browser itself is not touched.
func (f *fence) Close() error {
	f.once.Do(func() { close(f.done) })
	return f.c.Close()
}

const (
	fenceAllow = iota
	fenceSwallow
	fenceDeny
)

var deniedSessionPrefixes = []string{"Browser.", "Storage.", "SystemInfo.", "Target."}

// deniedCommands are session-level commands outside the denied domains that
// would still reach the user's wider browser state.
var deniedCommands = map[string]bool{
	"Network.getAllCookies": true, "Network.getCookies": true, "Network.setCookie": true, "Network.setCookies": true,
	"Network.deleteCookies": true, "Network.clearBrowserCookies": true, "Network.clearBrowserCache": true,
}

func parseParams(m *cdproto.Message, v any) bool {
	return len(m.Params) > 0 && json.Unmarshal([]byte(m.Params), v) == nil
}

func (f *fence) owns(s target.SessionID) (fsession, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fs, ok := f.sessions[s]
	return fs, ok
}

func (f *fence) checkOut(m *cdproto.Message) (verdict int, result, why string) {
	method := string(m.Method)
	if m.SessionID == "" {
		switch method {
		case "Target.createTarget":
			var p struct {
				URL              string `json:"url"`
				BrowserContextID string `json:"browserContextId"`
			}
			if !parseParams(m, &p) || (p.URL != "about:blank" && p.URL != "") || p.BrowserContextID != "" {
				return fenceDeny, "", "opening anything but a blank tab in your default profile"
			}
			f.learn(m.ID, "create")
		case "Target.attachToTarget":
			var p struct {
				TargetID target.ID `json:"targetId"`
			}
			if !parseParams(m, &p) || !f.pol.has(p.TargetID) {
				return fenceDeny, "", "attaching to a tab werkz did not open"
			}
			f.learn(m.ID, "attach:"+string(p.TargetID))
		case "Target.closeTarget":
			var p struct {
				TargetID target.ID `json:"targetId"`
			}
			if !parseParams(m, &p) || !f.pol.has(p.TargetID) {
				return fenceDeny, "", "closing a tab werkz did not open"
			}
		case "Target.detachFromTarget":
			var p struct {
				SessionID target.SessionID `json:"sessionId"`
			}
			if !parseParams(m, &p) {
				return fenceDeny, "", method
			}
			if _, ok := f.owns(p.SessionID); !ok {
				return fenceDeny, "", method + " on a session werkz did not open"
			}
		case "Target.setDiscoverTargets":
			// allowed: the replies and events are filtered on the way back
		case "Target.getTargets":
			return fenceSwallow, `{"targetInfos":[]}`, ""
		default:
			return fenceDeny, "", method
		}
		return fenceAllow, "", ""
	}
	if _, ok := f.owns(m.SessionID); !ok {
		return fenceDeny, "", method + " on a session werkz did not open"
	}
	switch method {
	case "Target.setAutoAttach":
		return fenceAllow, "", ""
	case "Target.setDiscoverTargets":
		return fenceSwallow, `{}`, "" // would report the user's other tabs
	case "Target.getTargets":
		return fenceSwallow, `{"targetInfos":[]}`, ""
	}
	if deniedCommands[method] {
		return fenceDeny, "", method
	}
	for _, p := range deniedSessionPrefixes {
		if strings.HasPrefix(method, p) {
			return fenceDeny, "", method
		}
	}
	return fenceAllow, "", ""
}

func (f *fence) learn(id int64, what string) {
	f.mu.Lock()
	f.pend[id] = what
	f.mu.Unlock()
}

// fenceOwnIDBase starts the message IDs of commands the fence sends itself,
// far above anything chromedp counts up to.
const fenceOwnIDBase = int64(1) << 40

// judgeDownload asks the policy about a download of one of werkz's targets
// and cancels it (Browser.cancelDownload for exactly that GUID, sent by the
// fence itself, never by werkz) when the answer is no. The page cannot send or
// widen this: it only ever cancels. Page.downloadWillBegin is not in the
// pinned cdproto, so it is read raw.
func (f *fence) judgeDownload(m *cdproto.Message) {
	var d struct {
		GUID              string `json:"guid"`
		URL               string `json:"url"`
		SuggestedFilename string `json:"suggestedFilename"`
	}
	if !parseParams(m, &d) || d.GUID == "" {
		return
	}
	f.pol.mu.Lock()
	judge := f.pol.download
	f.pol.mu.Unlock()
	if judge != nil && judge(d.SuggestedFilename, d.URL) {
		return
	}
	params, _ := json.Marshal(map[string]string{"guid": d.GUID})
	f.mu.Lock()
	f.ownSeq++
	id := fenceOwnIDBase + f.ownSeq
	f.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = f.c.Write(ctx, &cdproto.Message{ID: id, Method: "Browser.cancelDownload", Params: jsonv2.Value(params)})
	}()
}

type targetEvent struct {
	TargetInfo struct {
		TargetID target.ID `json:"targetId"`
		OpenerID target.ID `json:"openerId"`
	} `json:"targetInfo"`
	TargetID  target.ID        `json:"targetId"`
	SessionID target.SessionID `json:"sessionId"`
}

// filterIn updates the fence's knowledge from a message and reports whether
// werkz may see it.
func (f *fence) filterIn(m *cdproto.Message) bool {
	if m.ID >= fenceOwnIDBase {
		return false // the answer to a command the fence sent itself
	}
	if m.ID != 0 {
		f.mu.Lock()
		what := f.pend[m.ID]
		delete(f.pend, m.ID)
		f.mu.Unlock()
		if what == "" || m.Error != nil {
			return true
		}
		var res struct {
			TargetID  target.ID        `json:"targetId"`
			SessionID target.SessionID `json:"sessionId"`
		}
		if json.Unmarshal([]byte(m.Result), &res) != nil {
			return true
		}
		switch {
		case what == "create":
			f.pol.addTab(res.TargetID)
		case strings.HasPrefix(what, "attach:"):
			f.mu.Lock()
			f.sessions[res.SessionID] = fsession{root: target.ID(strings.TrimPrefix(what, "attach:"))}
			f.mu.Unlock()
		}
		return true
	}
	var ev targetEvent
	switch string(m.Method) {
	case "Target.targetCreated", "Target.targetInfoChanged":
		if !parseParams(m, &ev) {
			return false
		}
		id := ev.TargetInfo.TargetID
		if f.pol.has(id) {
			return true
		}
		if f.pol.has(ev.TargetInfo.OpenerID) {
			f.pol.addPopup(id) // opened by werkz's tab
			return true
		}
		return false // somebody else's tab: never reaches werkz
	case "Target.targetDestroyed", "Target.targetCrashed":
		if !parseParams(m, &ev) || !f.pol.has(ev.TargetID) {
			return false
		}
		if string(m.Method) == "Target.targetDestroyed" {
			if f.pol.isTab(ev.TargetID) {
				if f.pol.dropTab(ev.TargetID) == 0 {
					f.pol.markLost() // the last of werkz's tabs is gone
				}
			} else {
				f.pol.remove(ev.TargetID)
			}
		}
		return true
	case "Target.attachedToTarget":
		if !parseParams(m, &ev) {
			return false
		}
		if m.SessionID == "" {
			if !f.pol.has(ev.TargetInfo.TargetID) {
				return false
			}
			f.mu.Lock()
			f.sessions[ev.SessionID] = fsession{root: ev.TargetInfo.TargetID}
			f.mu.Unlock()
			return true
		}
		parent, ok := f.owns(m.SessionID)
		if !ok {
			return false
		}
		f.mu.Lock()
		f.sessions[ev.SessionID] = fsession{root: parent.root, child: true}
		f.mu.Unlock()
		return true
	case "Target.detachedFromTarget":
		if !parseParams(m, &ev) {
			return false
		}
		if _, ok := f.owns(ev.SessionID); !ok {
			return false
		}
		f.mu.Lock()
		delete(f.sessions, ev.SessionID)
		f.mu.Unlock()
		return true
	case "Target.receivedMessageFromTarget":
		return false
	}
	if m.SessionID != "" && string(m.Method) == "Page.downloadWillBegin" {
		if _, ok := f.owns(m.SessionID); ok {
			f.judgeDownload(m)
		}
	}
	if m.SessionID == "" {
		return false // browser-wide events are none of werkz's business
	}
	_, ok := f.owns(m.SessionID)
	return ok
}
