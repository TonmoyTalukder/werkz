package browser

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/chromedp/cdproto"
	"github.com/chromedp/cdproto/target"
)

// mux shares one connection to the user's Chrome between werkz's driver (chromedp) and its popup
// guard. Chrome asks the user to allow every app connection to a Chrome they opened themselves,
// so two connections would mean two questions.
//
// Each side gets a conn of its own that behaves like a separate connection: command ids are
// renumbered on the way out and restored on the way back, events of a session go to the side
// that attached it, and target discovery events go only to the sides that asked for them.
type mux struct {
	c conn

	wmu sync.Mutex // one writer at a time on the socket

	mu      sync.Mutex
	next    int64
	pending map[int64]muxPend
	owner   map[target.SessionID]*muxConn
	held    map[target.SessionID]*cdproto.Message // attach events whose side is not known yet
	subs    []*muxConn
	done    chan struct{}
	err     error
}

type muxPend struct {
	sub    *muxConn
	id     int64     // the id the side used
	method string    // to learn from the answer
	attach target.ID // Target.attachToTarget: the target
}

type muxConn struct {
	m      *mux
	once   sync.Once
	closed chan struct{}

	qmu   sync.Mutex // the side's inbox: never full, so one slow side cannot stall the other
	queue []*cdproto.Message
	ready chan struct{}

	discover bool // asked for Target.setDiscoverTargets
}

func newMux(c conn) *mux {
	m := &mux{c: c, pending: map[int64]muxPend{}, owner: map[target.SessionID]*muxConn{}, held: map[target.SessionID]*cdproto.Message{}, done: make(chan struct{})}
	go m.readLoop()
	return m
}

// conn hands out another side of the connection. The socket closes when every side has closed.
func (m *mux) conn() *muxConn {
	s := &muxConn{m: m, closed: make(chan struct{}), ready: make(chan struct{}, 1)}
	m.mu.Lock()
	m.subs = append(m.subs, s)
	m.mu.Unlock()
	return s
}

func (m *mux) readLoop() {
	defer close(m.done)
	for {
		msg := new(cdproto.Message)
		if err := m.c.Read(context.Background(), msg); err != nil {
			m.mu.Lock()
			m.err = err
			m.mu.Unlock()
			return
		}
		for _, d := range m.route(msg) {
			d.to.deliver(d.msg)
		}
	}
}

type muxDelivery struct {
	to  *muxConn
	msg *cdproto.Message
}

// route finds the sides a message from Chrome is for, restoring a command's id.
func (m *mux) route(msg *cdproto.Message) []muxDelivery {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []muxDelivery
	for _, s := range m.routeLocked(msg) {
		out = append(out, muxDelivery{s, msg})
	}
	if msg.ID != 0 && len(out) == 1 && len(m.held) > 0 {
		var res struct {
			SessionID target.SessionID `json:"sessionId"`
		}
		if json.Unmarshal([]byte(msg.Result), &res) == nil {
			if ev := m.held[res.SessionID]; ev != nil { // the attach event first, as Chrome sent it
				delete(m.held, res.SessionID)
				out = append([]muxDelivery{{out[0].to, ev}}, out...)
			}
		}
	}
	return out
}

func (m *mux) routeLocked(msg *cdproto.Message) []*muxConn {
	if msg.ID != 0 {
		p, ok := m.pending[msg.ID]
		if !ok {
			return nil
		}
		delete(m.pending, msg.ID)
		msg.ID = p.id
		if p.method == "Target.attachToTarget" && msg.Error == nil {
			var res struct {
				SessionID target.SessionID `json:"sessionId"`
			}
			if json.Unmarshal([]byte(msg.Result), &res) == nil && res.SessionID != "" {
				m.owner[res.SessionID] = p.sub
			}
		}
		return []*muxConn{p.sub}
	}
	if msg.SessionID != "" {
		s := m.owner[msg.SessionID]
		if s == nil {
			return nil
		}
		if msg.Method == "Target.attachedToTarget" { // a frame or worker of that side's page
			var ev struct {
				SessionID target.SessionID `json:"sessionId"`
			}
			if parseParams(msg, &ev) && ev.SessionID != "" {
				m.owner[ev.SessionID] = s
			}
		}
		return []*muxConn{s}
	}
	switch msg.Method {
	case "Target.attachedToTarget": // Chrome reports an attach before it answers the command
		var ev struct {
			SessionID  target.SessionID `json:"sessionId"`
			TargetInfo struct {
				TargetID target.ID `json:"targetId"`
			} `json:"targetInfo"`
		}
		if !parseParams(msg, &ev) {
			return nil
		}
		if s := m.owner[ev.SessionID]; s != nil {
			return []*muxConn{s}
		}
		var by *muxConn
		for _, p := range m.pending {
			if p.attach != "" && p.attach == ev.TargetInfo.TargetID {
				if by != nil && by != p.sub {
					m.held[ev.SessionID] = msg // both sides attaching to one target: the answer tells
					return nil
				}
				by = p.sub
			}
		}
		if by == nil {
			return nil
		}
		m.owner[ev.SessionID] = by
		return []*muxConn{by}
	case "Target.detachedFromTarget":
		var ev struct {
			SessionID target.SessionID `json:"sessionId"`
		}
		if !parseParams(msg, &ev) {
			return nil
		}
		s := m.owner[ev.SessionID]
		delete(m.owner, ev.SessionID)
		delete(m.held, ev.SessionID)
		if s == nil {
			return nil
		}
		return []*muxConn{s}
	case "Target.targetCreated", "Target.targetInfoChanged", "Target.targetDestroyed", "Target.targetCrashed":
		var out []*muxConn
		for _, s := range m.subs {
			if s.discover {
				out = append(out, s)
			}
		}
		return out
	}
	return append([]*muxConn(nil), m.subs...)
}

func (s *muxConn) deliver(msg *cdproto.Message) {
	c := *msg
	s.qmu.Lock()
	s.queue = append(s.queue, &c)
	s.qmu.Unlock()
	select {
	case s.ready <- struct{}{}:
	default:
	}
}

func (s *muxConn) take(msg *cdproto.Message) bool {
	s.qmu.Lock()
	defer s.qmu.Unlock()
	if len(s.queue) == 0 {
		return false
	}
	*msg = *s.queue[0]
	s.queue[0] = nil
	s.queue = s.queue[1:]
	return true
}

func (s *muxConn) Read(ctx context.Context, msg *cdproto.Message) error {
	for {
		if s.take(msg) {
			return nil
		}
		select {
		case <-s.ready:
		case <-s.closed:
			return io.EOF
		case <-ctx.Done():
			return ctx.Err()
		case <-s.m.done:
			if s.take(msg) { // what arrived before the socket closed
				return nil
			}
			s.m.mu.Lock()
			err := s.m.err
			s.m.mu.Unlock()
			if err == nil {
				err = io.EOF
			}
			return err
		}
	}
}

func (s *muxConn) Write(ctx context.Context, msg *cdproto.Message) error {
	select {
	case <-s.closed:
		return errors.New("connection closed")
	default:
	}
	m := s.m
	p := muxPend{sub: s, id: msg.ID, method: string(msg.Method)}
	switch p.method {
	case "Target.attachToTarget":
		var a struct {
			TargetID target.ID `json:"targetId"`
		}
		if parseParams(msg, &a) {
			p.attach = a.TargetID
		}
	case "Target.setDiscoverTargets":
		var d struct {
			Discover bool `json:"discover"`
		}
		if parseParams(msg, &d) {
			m.mu.Lock()
			s.discover = d.Discover
			m.mu.Unlock()
		}
	}
	out := *msg
	m.mu.Lock()
	m.next++
	out.ID = m.next
	m.pending[out.ID] = p
	m.mu.Unlock()
	m.wmu.Lock()
	err := m.c.Write(ctx, &out)
	m.wmu.Unlock()
	if err != nil {
		m.mu.Lock()
		delete(m.pending, out.ID)
		m.mu.Unlock()
	}
	return err
}

// Close closes this side; the last side to close closes the socket.
func (s *muxConn) Close() error {
	last := false
	s.once.Do(func() {
		close(s.closed)
		m := s.m
		m.mu.Lock()
		for id, p := range m.pending {
			if p.sub == s {
				delete(m.pending, id)
			}
		}
		for sess, o := range m.owner {
			if o == s {
				delete(m.owner, sess)
			}
		}
		for i, x := range m.subs {
			if x == s {
				m.subs = append(m.subs[:i], m.subs[i+1:]...)
				break
			}
		}
		last = len(m.subs) == 0
		m.mu.Unlock()
	})
	if last {
		return s.m.c.Close()
	}
	return nil
}
