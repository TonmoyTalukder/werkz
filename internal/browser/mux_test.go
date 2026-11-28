package browser

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto"
)

func readMux(t *testing.T, c *muxConn) *cdproto.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m := new(cdproto.Message)
	if err := c.Read(ctx, m); err != nil {
		t.Fatalf("read: %v", err)
	}
	return m
}

func noMux(t *testing.T, c *muxConn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	m := new(cdproto.Message)
	if err := c.Read(ctx, m); err == nil {
		t.Fatalf("unexpected message %s %s", m.Method, m.Params)
	}
}

func lastSent(t *testing.T, fc *fakeConn) *cdproto.Message {
	t.Helper()
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if len(fc.sent) == 0 {
		t.Fatal("nothing sent")
	}
	return fc.sent[len(fc.sent)-1]
}

func TestMuxSharesOneConnection(t *testing.T) {
	fc := newFakeConn()
	m := newMux(fc)
	drv, grd := m.conn(), m.conn()
	ctx := context.Background()

	// Both sides use id 1: each gets its own answer back under its own id.
	_ = drv.Write(ctx, msg(1, "", "Target.createTarget", `{"url":"about:blank"}`))
	a := lastSent(t, fc).ID
	_ = grd.Write(ctx, msg(1, "", "Target.setDiscoverTargets", `{"discover":true}`))
	b := lastSent(t, fc).ID
	if a == b {
		t.Fatalf("ids not renumbered: %d %d", a, b)
	}
	fc.in <- msg(b, "", "", "")
	fc.in <- msg(a, "", "", "")
	if got := readMux(t, grd); got.ID != 1 {
		t.Errorf("guard answer id %d", got.ID)
	}
	if got := readMux(t, drv); got.ID != 1 {
		t.Errorf("driver answer id %d", got.ID)
	}

	// Discovery events reach only the side that asked for them.
	fc.in <- msg(0, "", "Target.targetCreated", `{"targetInfo":{"targetId":"T1","type":"page"}}`)
	if got := readMux(t, grd); got.Method != "Target.targetCreated" {
		t.Errorf("guard got %s", got.Method)
	}
	noMux(t, drv)

	// Attaches: the browser-level event, sent before the answer, goes to the side attaching.
	_ = drv.Write(ctx, msg(2, "", "Target.attachToTarget", `{"targetId":"T1","flatten":true}`))
	da := lastSent(t, fc).ID
	_ = grd.Write(ctx, msg(2, "", "Target.attachToTarget", `{"targetId":"T2","flatten":true}`))
	ga := lastSent(t, fc).ID
	fc.in <- msg(0, "", "Target.attachedToTarget", `{"sessionId":"S1","targetInfo":{"targetId":"T1"}}`)
	fc.in <- msg(da, "", "", "")
	fc.in <- msg(ga, "", "", "")
	if got := readMux(t, drv); got.Method != "Target.attachedToTarget" {
		t.Errorf("driver got %s", got.Method)
	}
	readMux(t, drv)
	if got := readMux(t, grd); got.ID != 2 {
		t.Errorf("guard got %+v", got)
	}
	m.mu.Lock()
	m.owner["S2"] = grd // as the guard's attach answer would have taught
	m.mu.Unlock()

	// Session events go to the session's side, frames of a page too.
	fc.in <- msg(0, "S1", "Page.loadEventFired", `{}`)
	fc.in <- msg(0, "S2", "Fetch.requestPaused", `{}`)
	fc.in <- msg(0, "S1", "Target.attachedToTarget", `{"sessionId":"S1F","targetInfo":{"targetId":"F1"}}`)
	fc.in <- msg(0, "S1F", "Network.requestWillBeSent", `{}`)
	for _, want := range []string{"Page.loadEventFired", "Target.attachedToTarget", "Network.requestWillBeSent"} {
		if got := readMux(t, drv); string(got.Method) != want {
			t.Errorf("driver got %s, want %s", got.Method, want)
		}
	}
	if got := readMux(t, grd); got.Method != "Fetch.requestPaused" {
		t.Errorf("guard got %s", got.Method)
	}
	noMux(t, drv)
	noMux(t, grd)

	// Both sides attaching to one target: the event waits for the answer that names its session.
	_ = drv.Write(ctx, msg(4, "", "Target.attachToTarget", `{"targetId":"T9","flatten":true}`))
	d9 := lastSent(t, fc).ID
	_ = grd.Write(ctx, msg(4, "", "Target.attachToTarget", `{"targetId":"T9","flatten":true}`))
	g9 := lastSent(t, fc).ID
	fc.in <- msg(0, "", "Target.attachedToTarget", `{"sessionId":"G9","targetInfo":{"targetId":"T9"}}`)
	fc.in <- msg(0, "", "Target.attachedToTarget", `{"sessionId":"D9","targetInfo":{"targetId":"T9"}}`)
	g := msg(g9, "", "", "")
	g.Result = []byte(`{"sessionId":"G9"}`)
	d := msg(d9, "", "", "")
	d.Result = []byte(`{"sessionId":"D9"}`)
	fc.in <- g
	fc.in <- d
	for _, c := range []struct {
		side *muxConn
		sess string
	}{{grd, "G9"}, {drv, "D9"}} {
		ev := readMux(t, c.side)
		if ev.Method != "Target.attachedToTarget" || !strings.Contains(string(ev.Params), c.sess) {
			t.Errorf("%s: got %s %s", c.sess, ev.Method, ev.Params)
		}
		if ans := readMux(t, c.side); ans.ID != 4 {
			t.Errorf("%s: answer %+v", c.sess, ans)
		}
	}

	// The socket closes only when both sides have.
	_ = grd.Close()
	if err := grd.Write(ctx, msg(3, "", "Target.getTargets", "")); err == nil {
		t.Error("write after close")
	}
	_ = drv.Write(ctx, msg(3, "", "Target.getTargets", ""))
	if lastSent(t, fc).Method != "Target.getTargets" {
		t.Error("driver cut off by the guard closing")
	}
	_ = drv.Close()
	close(fc.in)
	select {
	case <-m.done:
	case <-time.After(2 * time.Second):
		t.Fatal("mux did not stop")
	}
}
