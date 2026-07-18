package httpserver

import (
	"context"
	"sync"
)

type xrayLifecycleMode uint8

const (
	xrayLifecycleIdle xrayLifecycleMode = iota
	xrayLifecycleStarts
	xrayLifecycleExclusive
)

// xrayLifecycleGate lets concurrent start requests reach Manager, which owns
// the official "already in progress" response, while keeping plugin and stop
// mutations exclusive with every admitted start.
type xrayLifecycleGate struct {
	mu               sync.Mutex
	changed          chan struct{}
	mode             xrayLifecycleMode
	activeStarts     int
	waitingExclusive int
}

func (g *xrayLifecycleGate) acquireStart(ctx context.Context) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		g.mu.Lock()
		g.initializeLocked()
		if g.waitingExclusive == 0 && (g.mode == xrayLifecycleStarts || g.mode == xrayLifecycleIdle) {
			g.mode = xrayLifecycleStarts
			g.activeStarts++
			g.mu.Unlock()
			if ctx.Err() != nil {
				g.releaseStart()
				return false
			}
			return true
		}
		changed := g.changed
		g.mu.Unlock()

		select {
		case <-changed:
		case <-ctx.Done():
			return false
		}
	}
}

func (g *xrayLifecycleGate) releaseStart() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.mode != xrayLifecycleStarts || g.activeStarts == 0 {
		panic("httpserver: release unheld Xray start lifecycle lease")
	}
	g.activeStarts--
	if g.activeStarts == 0 {
		g.mode = xrayLifecycleIdle
		g.notifyLocked()
	}
}

func (g *xrayLifecycleGate) acquireExclusive(ctx context.Context) bool {
	if ctx == nil {
		ctx = context.Background()
	}

	g.mu.Lock()
	g.initializeLocked()
	g.waitingExclusive++
	for {
		if g.mode == xrayLifecycleIdle {
			g.waitingExclusive--
			g.mode = xrayLifecycleExclusive
			g.mu.Unlock()
			if ctx.Err() != nil {
				g.releaseExclusive()
				return false
			}
			return true
		}
		changed := g.changed
		g.mu.Unlock()

		select {
		case <-changed:
			g.mu.Lock()
		case <-ctx.Done():
			g.mu.Lock()
			g.waitingExclusive--
			if g.mode == xrayLifecycleIdle && g.waitingExclusive == 0 {
				g.notifyLocked()
			}
			g.mu.Unlock()
			return false
		}
	}
}

func (g *xrayLifecycleGate) releaseExclusive() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.mode != xrayLifecycleExclusive {
		panic("httpserver: release unheld exclusive Xray lifecycle lease")
	}
	g.mode = xrayLifecycleIdle
	g.notifyLocked()
}

func (g *xrayLifecycleGate) initializeLocked() {
	if g.changed == nil {
		g.changed = make(chan struct{})
	}
}

func (g *xrayLifecycleGate) notifyLocked() {
	g.initializeLocked()
	close(g.changed)
	g.changed = make(chan struct{})
}

func (g *xrayLifecycleGate) snapshot() (xrayLifecycleMode, int, int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.mode, g.activeStarts, g.waitingExclusive
}
