// Package cepmatch keeps per-Key matcher state: a pending-A queue (relaxed)
// or the previous event (strict). Depends only on cepwin.
package cepmatch

import (
	"sync"

	"ontology/cepwin"
)

// Match is one emitted (A, B) pair.
type Match struct {
	A cepwin.Event
	B cepwin.Event
}

type pendingQ struct {
	items []cepwin.Event
	head  int
}

type Engine struct {
	mode       cepwin.Mode
	t          int64
	maxPending int
	mu         sync.Mutex
	queues     map[string]*pendingQ
	last       map[string]*cepwin.Event // nil means the Key is unseen
	matches    []Match
	// checked: As examined for the most recent event; unexported and never
	// returned by any method. White-box tests read it directly.
	checked int
}

func New(mode cepwin.Mode, t int64, maxPending int) (*Engine, error) {
	if err := cepwin.ValidateParams(mode, t, maxPending); err != nil {
		return nil, err
	}
	return &Engine{mode: mode, t: t, maxPending: maxPending,
		queues: map[string]*pendingQ{}, last: map[string]*cepwin.Event{}}, nil
}

// Feed pushes a batch; any rejection restores entry state (invariant 4).
func (e *Engine) Feed(evs []cepwin.Event) ([]Match, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	sq := map[string]*pendingQ{}
	for k, q := range e.queues {
		sq[k] = &pendingQ{items: append([]cepwin.Event(nil), q.items[q.head:]...)}
	}
	sl := map[string]*cepwin.Event{}
	for k, p := range e.last {
		v := *p
		sl[k] = &v
	}
	sm := append([]Match(nil), e.matches...)
	out := []Match{}
	for _, ev := range evs {
		m, err := e.step(ev)
		if err != nil {
			e.queues, e.last, e.matches = sq, sl, sm
			return nil, err
		}
		if m != nil {
			out = append(out, *m)
		}
	}
	return out, nil
}

func (e *Engine) step(ev cepwin.Event) (*Match, error) {
	if err := cepwin.ValidateEvent(ev); err != nil {
		return nil, err
	}
	if p := e.last[ev.Key]; p != nil && ev.TS < p.TS {
		return nil, cepwin.ErrTimeRegression
	}
	e.checked = 0
	var m *Match
	var err error
	if e.mode == cepwin.Relaxed {
		m, err = e.relaxed(ev)
	} else if a := e.last[ev.Key]; ev.Type == "B" && a != nil && a.Type == "A" && cepwin.InWindow(a.TS, ev.TS, e.t) {
		mm := Match{A: *a, B: ev}
		e.matches = append(e.matches, mm)
		m = &mm
	}
	e.last[ev.Key] = &ev
	return m, err
}

func (e *Engine) relaxed(ev cepwin.Event) (*Match, error) {
	q := e.queues[ev.Key]
	if q == nil {
		q = &pendingQ{}
		e.queues[ev.Key] = q
	}
	for q.head < len(q.items) {
		e.checked++
		if !cepwin.Expired(q.items[q.head].TS, ev.TS, e.t) {
			break
		}
		q.head++
	}
	if q.head == len(q.items) {
		q.items, q.head = q.items[:0], 0
	}
	switch ev.Type {
	case "A":
		if len(q.items)-q.head >= e.maxPending {
			return nil, cepwin.ErrQueueFull
		}
		q.items = append(q.items, ev)
	case "B":
		if q.head < len(q.items) {
			a := q.items[q.head]
			e.checked++
			if cepwin.InWindow(a.TS, ev.TS, e.t) {
				q.head++ // consume: this A never matches again
				mm := Match{A: a, B: ev}
				e.matches = append(e.matches, mm)
				return &mm, nil
			}
		}
	}
	return nil, nil
}

func (e *Engine) Matches() []Match {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Match(nil), e.matches...)
}

// VerifyHeadOnly seeds m same-TS As then one B and fails unless the
// examined-A count stayed head-bounded. Only an error leaves the package.
func VerifyHeadOnly(m int) error {
	e, _ := New(cepwin.Relaxed, 5, m+1)
	evs := make([]cepwin.Event, 0, m+1)
	for range m {
		evs = append(evs, cepwin.Event{Key: "k", Type: "A", TS: 1})
	}
	if _, err := e.Feed(append(evs, cepwin.Event{Key: "k", Type: "B", TS: 1})); err != nil {
		return err
	}
	if e.checked > 3 { // 1 consumed + small constant, independent of m
		return cepwin.ErrQueueFull
	}
	return nil
}
