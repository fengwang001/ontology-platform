// Package api is the public, concurrency-safe entry point for SLA monitoring.
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/mon"
)

// Pairwise-distinct sentinel errors for the four failure modes.
var (
	ErrInvalidParam   = errors.New("sla: invalid parameter")       // t<0, w<1, k<1 or k>w
	ErrDuplicateBegin = errors.New("sla: duplicate begin")         // id already in flight
	ErrUnknown        = errors.New("sla: end for unknown request") // never began, or ended
	ErrNegative       = errors.New("sla: negative latency")        // End.ts earlier than Begin.ts
)

// Monitor records request lifecycles, statistics and the alarm window.
type Monitor struct {
	mu       sync.RWMutex
	inFlight map[string]int64 // id -> begin timestamp of requests in flight
	m        *mon.Monitor
}

// New builds a Monitor with threshold T, a window of the w latest
// completions, and breach when violations in the window reach k.
func New(t int64, w, k int) (*Monitor, error) {
	if t < 0 || w < 1 || k < 1 || k > w {
		return nil, ErrInvalidParam
	}
	return &Monitor{inFlight: make(map[string]int64), m: mon.NewMonitor(t, w, k)}, nil
}

// Begin registers request id starting at ts; a duplicate in-flight id is
// rejected before any state change.
func (x *Monitor) Begin(id string, ts int64) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if _, ok := x.inFlight[id]; ok {
		return ErrDuplicateBegin
	}
	x.inFlight[id] = ts
	x.m.Begin()
	return nil
}

// End records request id finishing at ts. Unknown ids and negative latencies
// are rejected before any state (even the in-flight entry) changes.
func (x *Monitor) End(id string, ts int64) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	begin, ok := x.inFlight[id]
	if !ok {
		return ErrUnknown
	}
	if ts < begin {
		return ErrNegative // entry stays: the request remains in flight
	}
	delete(x.inFlight, id)
	x.m.Complete(ts - begin)
	return nil
}

func (x *Monitor) Count() int64      { x.mu.RLock(); defer x.mu.RUnlock(); return x.m.Count() }
func (x *Monitor) Violations() int64 { x.mu.RLock(); defer x.mu.RUnlock(); return x.m.Violations() }
func (x *Monitor) InFlight() int     { x.mu.RLock(); defer x.mu.RUnlock(); return x.m.InFlight() }
func (x *Monitor) Breached() bool    { x.mu.RLock(); defer x.mu.RUnlock(); return x.m.Breached() }
func (x *Monitor) AvgLatency() float64 {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.m.Avg()
}

// MinLatency / MaxLatency are 0 before any completion.
func (x *Monitor) MinLatency() int64 { x.mu.RLock(); defer x.mu.RUnlock(); v, _ := x.m.Min(); return v }
func (x *Monitor) MaxLatency() int64 { x.mu.RLock(); defer x.mu.RUnlock(); v, _ := x.m.Max(); return v }

type snapshot struct {
	count, viol, min, max int64
	avg                   float64
	inFlight              int
	breached              bool
}

func snap(x *Monitor) snapshot {
	return snapshot{x.Count(), x.Violations(), x.MinLatency(), x.MaxLatency(),
		x.AvgLatency(), x.InFlight(), x.Breached()}
}

// SelfCheck replays built-in sequences and verifies all four invariants; nil means pass.
func SelfCheck() error {
	x, _ := New(10, 4, 3)
	steps := []struct {
		id       string
		b, e     int64
		viol, br bool
	}{
		{"A", 0, 10, false, false}, {"B", 20, 35, true, false},
		{"C", 40, 45, false, false}, {"D", 50, 61, true, false},
		{"E", 70, 82, true, true}, {"F", 90, 94, false, false},
		{"G", 100, 120, true, true},
	}
	var v int64
	for _, s := range steps {
		if err := x.Begin(s.id, s.b); err != nil {
			return err
		}
		if err := x.End(s.id, s.e); err != nil {
			return err
		}
		if s.viol {
			v++
		}
		if x.Violations() != v || x.Breached() != s.br {
			return fmt.Errorf("step %s: v=%d br=%v want %d/%v", s.id, x.Violations(), x.Breached(), v, s.br)
		}
	}
	if s := snap(x); s != (snapshot{7, 4, 4, 20, 11, 0, true}) { // lats 10,15,5,11,12,4,20; window D,E,F,G
		return fmt.Errorf("final stats mismatch: %+v", s)
	}
	y, _ := New(10, 1, 1) // in-flight never counts; ==T OK, ==T+1 violates
	if y.Begin("p", 0) != nil || y.Count() != 0 || y.InFlight() != 1 {
		return errors.New("in-flight request entered statistics")
	}
	if y.End("p", 10) != nil || y.Violations() != 0 {
		return errors.New("latency == T must be OK")
	}
	if err := y.Begin("q", 0); err != nil || y.End("q", 11) != nil || y.Violations() != 1 {
		return errors.New("latency == T+1 must violate")
	}
	if y.Begin("dup", 0) != nil || y.Begin("r", 5) != nil {
		return errors.New("fixture begin failed")
	}
	before := snap(y) // every rejection fails distinctly and leaves no trace
	for _, c := range []struct {
		fn   func() error
		want error
	}{
		{func() error { return y.Begin("dup", 1) }, ErrDuplicateBegin},
		{func() error { return y.End("nope", 2) }, ErrUnknown},
		{func() error { return y.End("r", 4) }, ErrNegative},
	} {
		if err := c.fn(); !errors.Is(err, c.want) || snap(y) != before {
			return fmt.Errorf("reject %v: err=%v changed=%v", c.want, err, snap(y) != before)
		}
	}
	return nil
}
