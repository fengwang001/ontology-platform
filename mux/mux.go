// Package mux implements a request-response correlation matcher: callers
// register a correlation id, responses arrive out of order, and each
// response is dispatched to the matching waiter.
package mux

import (
	"sync"
	"time"
)

// Mux matches responses to pending requests by correlation id.
type Mux struct {
	mu      sync.Mutex
	now     func() time.Time
	pending map[string]*waiter
	seen    map[string]struct{}
	closed  bool

	delivered int
	orphans   int
	late      int
	timedOut  int
	closedOut int
}

// New returns a Mux whose deadlines are judged by the injected clock.
// A nil now falls back to time.Now.
func New(now func() time.Time) *Mux {
	if now == nil {
		now = time.Now
	}
	return &Mux{
		now:     now,
		pending: make(map[string]*waiter),
		seen:    make(map[string]struct{}),
	}
}

// Register enrolls a waiter for id and returns the channel its response
// will arrive on. The channel is buffered and closed when the waiter
// completes for any reason. Registering an id that is still pending
// fails with ErrDuplicateID without disturbing the existing waiter.
func (m *Mux) Register(id string) (<-chan []byte, error) {
	w, err := m.register(id, time.Time{}, false)
	if err != nil {
		return nil, err
	}
	return w.ch, nil
}

// Wait blocks until the response for id arrives, the deadline (judged by
// the injected clock and enforced by Tick) passes, or the Mux is closed.
func (m *Mux) Wait(id string, deadline time.Time) ([]byte, error) {
	w, err := m.register(id, deadline, true)
	if err != nil {
		return nil, err
	}
	<-w.done
	return w.payload, w.err
}

func (m *Mux) register(id string, deadline time.Time, timed bool) (*waiter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrClosed
	}
	if _, ok := m.pending[id]; ok {
		return nil, ErrDuplicateID
	}
	w := newWaiter(id, deadline, timed)
	m.pending[id] = w
	m.seen[id] = struct{}{}
	return w, nil
}

// Deliver hands payload to the waiter registered for id, copying the
// slice so later mutation by the caller cannot affect the waiter. A
// response for an id that was never registered is counted as an orphan;
// one whose waiter already left is counted as late. Neither creates a
// pending slot.
func (m *Mux) Deliver(id string, payload []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.pending[id]
	if !ok {
		if _, known := m.seen[id]; known {
			m.late++
		} else {
			m.orphans++
		}
		return
	}
	delete(m.pending, id)
	m.delivered++
	w.finish(append([]byte(nil), payload...), nil)
}

// Tick expires every waiter whose deadline has been reached according to
// the injected clock, finishing it with ErrTimedOut.
func (m *Mux) Tick() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	for id, w := range m.pending {
		if w.timed && !now.Before(w.deadline) {
			delete(m.pending, id)
			m.timedOut++
			w.finish(nil, ErrTimedOut)
		}
	}
}

// Close finishes every pending waiter with ErrClosed and rejects future
// registrations. It is idempotent.
func (m *Mux) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.closed = true
	for id, w := range m.pending {
		delete(m.pending, id)
		m.closedOut++
		w.finish(nil, ErrClosed)
	}
}

// Stats returns a snapshot of the matcher's counters.
func (m *Mux) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Stats{
		Pending:   len(m.pending),
		Orphans:   m.orphans,
		Late:      m.late,
		Delivered: m.delivered,
	}
}
