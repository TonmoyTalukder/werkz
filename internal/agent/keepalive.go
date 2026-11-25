package agent

import (
	"context"
	"time"
)

const (
	keepAliveEvery     = 270 * time.Second // re-ping before the 5-minute TTL expires (timer starts at request start)
	keepAliveMinTokens = 4096              // below this, a cold re-write is cheap anyway
)

// startKeepAlive keeps an idle Claude prompt cache warm with max_tokens:0 pings (each bills a
// cache read, no output) for KeepAliveWindow after the last request, so a reply after a pause
// reads the cache instead of re-writing the whole conversation.
func (e *Engine) startKeepAlive() {
	e.StopKeepAlive()
	ka, at, mi, fast := e.lastKA, e.lastReqAt, e.lastMI, e.lastFast
	win := e.cfg.KeepAliveWindow()
	if ka == nil || win == 0 || e.lastPrefix < keepAliveMinTokens || e.cfg.TTL1h() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.kaMu.Lock()
	e.kaCancel = cancel
	e.kaMu.Unlock()
	go func() {
		deadline := at.Add(win)
		next := at.Add(keepAliveEvery)
		for next.Before(deadline) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Until(next)):
			}
			start := time.Now()
			u, err := ka(ctx)
			if err != nil {
				if ctx.Err() == nil {
					r, _ := Classify("anthropic", err)
					e.send(NoticeMsg{"warn", "Cache keep-alive stopped: " + r})
				}
				return
			}
			e.send(KeepAliveMsg{Usage: u, Cost: mi.Cost(u.In, 0, u.CacheRead, u.CacheWrite, fast, false), Until: start.Add(5 * time.Minute)})
			next = start.Add(keepAliveEvery)
		}
	}()
}

// StopKeepAlive stops pinging the cache. Safe to call from any goroutine.
func (e *Engine) StopKeepAlive() {
	e.kaMu.Lock()
	defer e.kaMu.Unlock()
	if e.kaCancel != nil {
		e.kaCancel()
		e.kaCancel = nil
	}
}

// KeepAliveActive reports whether an idle prompt cache is being kept warm.
func (e *Engine) KeepAliveActive() bool {
	e.kaMu.Lock()
	defer e.kaMu.Unlock()
	return e.kaCancel != nil
}
