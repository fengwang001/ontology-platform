// Package drain implements a graceful-shutdown gate for in-flight
// requests. It is not an HTTP server, connection pool, health check,
// or signal handler; it only coordinates admission and draining.
package drain

import (
	"sync"
	"time"
)

// Gate admits requests while running and, once Shutdown is called,
// rejects new requests and waits for in-flight ones to be released.
//
// The zero value is not usable; construct a Gate with New.
type Gate struct {
	now func() time.Time

	mu       sync.Mutex
	changed  chan struct{} // closed and replaced on every state change
	finished chan struct{} // closed when the first Shutdown completes

	admitted int
	rejected int
	inFlight int

	draining bool
	done     bool
	result   error
}

// New returns a Gate that reads the current time from now.
// A nil now falls back to time.Now.
func New(now func() time.Time) *Gate {
	if now == nil {
		now = time.Now
	}
	return &Gate{
		now:      now,
		changed:  make(chan struct{}),
		finished: make(chan struct{}),
	}
}

// Enter requests admission. On success it returns a release function
// that must be called exactly once; extra calls are no-ops. Once
// shutdown has started, Enter returns a nil release and ErrShuttingDown.
func (g *Gate) Enter() (release func(), err error) {
	g.mu.Lock()
	if g.draining {
		g.rejected++
		g.mu.Unlock()
		return nil, ErrShuttingDown
	}
	g.admitted++
	g.inFlight++
	g.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			g.inFlight--
			g.broadcastLocked()
			g.mu.Unlock()
		})
	}, nil
}

// Shutdown starts draining and blocks until every admitted request is
// released or the deadline is reached, as reported by the injected
// clock (re-evaluated on every state change and on Tick).
//
// Shutdown is idempotent: only the first call performs the wait, and
// every call, concurrent or later, returns that same result.
func (g *Gate) Shutdown(deadline time.Time) error {
	g.mu.Lock()
	if g.draining {
		finished := g.finished
		g.mu.Unlock()
		<-finished
		return g.result
	}
	g.draining = true

	for g.inFlight > 0 && g.now().Before(deadline) {
		changed := g.changed
		g.mu.Unlock()
		<-changed
		g.mu.Lock()
	}
	if g.inFlight > 0 {
		g.result = ErrDrainTimeout
	}
	g.done = true
	g.mu.Unlock()

	close(g.finished)
	return g.result
}

// Tick wakes the shutdown waiter so it re-evaluates the deadline
// against the injected clock. Call it after advancing a fake clock.
func (g *Gate) Tick() {
	g.mu.Lock()
	g.broadcastLocked()
	g.mu.Unlock()
}

// Stats returns a consistent snapshot of the gate's counters.
func (g *Gate) Stats() Stats {
	g.mu.Lock()
	defer g.mu.Unlock()
	return Stats{
		Admitted: g.admitted,
		Rejected: g.rejected,
		InFlight: g.inFlight,
		Done:     g.done,
	}
}

// broadcastLocked wakes every waiter. g.mu must be held.
func (g *Gate) broadcastLocked() {
	close(g.changed)
	g.changed = make(chan struct{})
}
