// Package api is the public broadcast-join facade (stdlib only).
package api

import (
	"errors"
	"fmt"
	"ontology/dim"
	"ontology/join"
	"sync"
)

type Entry = dim.Entry
type Fact = join.Fact
type Row = join.Row

var ErrEmptyKey = dim.ErrEmptyKey     // empty Key on an Entry or a Fact
var ErrEmptyBatch = dim.ErrEmptyBatch // Broadcast with zero entries
var ErrMaxBytes = dim.ErrMaxBytes     // New with maxBytes <= 0
var ErrFuture = dim.ErrFuture         // Join with Vsn > current V

// Engine serializes access; Join/View/Joined/SelfCheck are safe concurrently.
type Engine struct {
	mu              sync.RWMutex
	st              *dim.Store
	jr              *join.Joiner
	rows            []Row
	dropped, missed int64
}

func New(maxBytes int64) (*Engine, error) {
	st, err := dim.NewStore(maxBytes)
	if err != nil {
		return nil, err
	}
	return &Engine{st: st, jr: join.New(st)}, nil
}
func (e *Engine) Broadcast(b []Entry) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.st.Broadcast(b)
}

// Join resolves one fact against snapshot f.Vsn; miss/stale are legal results.
func (e *Engine) Join(f Fact) (Row, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, k := e.jr.Resolve(f)
	switch k {
	case join.ResultHit:
		e.rows = append(e.rows, r) // counters mutate only after validation
		return r, nil
	case join.ResultMiss:
		e.missed++
	case join.ResultStale:
		e.dropped++
	case join.ResultFuture:
		return Row{}, ErrFuture
	default:
		return Row{}, ErrEmptyKey
	}
	return Row{}, nil
}
func (e *Engine) View() map[string]string { e.mu.RLock(); defer e.mu.RUnlock(); return e.st.Current() }
func (e *Engine) Joined() []Row {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return append(make([]Row, 0, len(e.rows)), e.rows...)
}
func (e *Engine) Dropped() int64 { e.mu.RLock(); defer e.mu.RUnlock(); return e.dropped }
func (e *Engine) Missed() int64  { e.mu.RLock(); defer e.mu.RUnlock(); return e.missed }

type sc struct {
	B    []Entry
	F    Fact
	want Row
}

var builtin = []sc{
	{B: []Entry{{Key: "a", Val: "1"}, {Key: "b", Val: "2"}}},
	{B: []Entry{{Key: "c", Val: "3"}, {Key: "d", Val: "4"}}},
	{F: Fact{Key: "a", Vsn: 1}, want: Row{Key: "a", Val: "1", Vsn: 1}},
	{B: []Entry{{Key: "a", Val: "9"}}},
	{F: Fact{Key: "a", Vsn: 1}, want: Row{Key: "a", Val: "1", Vsn: 1}},
	{F: Fact{Key: "a", Vsn: 3}, want: Row{Key: "a", Val: "9", Vsn: 3}},
	{B: []Entry{{Key: "e", Val: "5"}, {Key: "f", Val: "6"}}},
	{F: Fact{Key: "a", Vsn: 1}}, // stale: v1 evicted at step 7
}

func state(g *Engine) string {
	return fmt.Sprint(g.st.V(), g.st.Used(), g.st.Versions(), len(g.Joined()), g.Dropped(), g.Missed())
}
func replay(steps []sc) (*Engine, []Row, error) {
	g, _ := New(100)
	var rows []Row
	for _, o := range steps {
		if o.B != nil {
			if _, err := g.Broadcast(o.B); err != nil {
				return nil, nil, err
			} else if g.st.Used() > 100 {
				return nil, nil, errors.New("used exceeded maxBytes")
			}
			continue
		}
		if r, err := g.Join(o.F); err != nil || r != o.want {
			return nil, nil, fmt.Errorf("step %+v -> %+v, %v", o.F, r, err)
		} else if r != (Row{}) {
			rows = append(rows, r)
		}
	}
	return g, rows, nil
}

// SelfCheck replays the built-in sequence on local engines (receiver untouched) and verifies the four invariants.
func (e *Engine) SelfCheck() error {
	x, rows, err := replay(builtin) // invariants 1 and 3
	if err != nil {
		return fmt.Errorf("selfcheck: %w", err)
	}
	// invariants 1,2,3: versioned row values; V monotone; cap, eviction order, floor.
	if s := fmt.Sprint(rows); s != "[{a 1 1} {a 1 1} {a 9 3}]" || state(x) != "4 90 [3 4] 3 1 0" {
		return fmt.Errorf("selfcheck mismatch: %s | %s", s, state(x))
	}
	if _, r2, er := replay(builtin); er != nil || fmt.Sprint(r2) != fmt.Sprint(rows) {
		return fmt.Errorf("selfcheck recompute: %v %v", r2, er) // invariant 1
	}
	z, _ := New(5) // invariants 2,3: reject at V,V-1 floor, full rollback
	if _, er := z.Broadcast([]Entry{{Key: "abcdefgh", Val: "x"}}); !errors.Is(er, dim.ErrSnapshotTooLarge) ||
		state(z) != "0 0 [0] 0 0 0" {
		return errors.New("selfcheck oversized reject left a trace")
	}
	if _, er := New(0); !errors.Is(er, ErrMaxBytes) { // invariant 4: distinct errors
		return errors.New("selfcheck New(0)")
	}
	bx := state(x)
	cases := []struct {
		w error
		f func() error
	}{
		{ErrEmptyBatch, func() error { _, e := x.Broadcast(nil); return e }},
		{ErrEmptyKey, func() error { _, e := x.Broadcast([]Entry{{Key: "", Val: "z"}}); return e }},
		{ErrEmptyKey, func() error { _, e := x.Join(Fact{}); return e }},
		{ErrFuture, func() error { _, e := x.Join(Fact{Key: "a", Vsn: x.st.V() + 1}); return e }},
	}
	for i, c := range cases {
		if er := c.f(); !errors.Is(er, c.w) || state(x) != bx { // invariant 4: no trace
			return fmt.Errorf("selfcheck call %d: %v", i, er)
		}
	}
	return nil
}
