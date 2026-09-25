// Package api exposes New/Add/Allocate/SelfCheck for the fair-share allocator.
package api

import (
	"errors"
	"sync"

	"ontology/alloc"
	"ontology/mf"
)

// Sentinel errors: each failure kind is distinct and judgeable with errors.Is.
var (
	ErrInvalidCapacity = alloc.ErrInvalidCapacity
	ErrEmptyID         = errors.New("api: task id must not be empty")
	ErrDuplicateID     = errors.New("api: task id already exists")
	ErrNegativeDemand  = errors.New("api: demand must not be negative")
)

// Allocator is a concurrency-safe, in-process max-min fair-share allocator.
type Allocator struct {
	mu    sync.Mutex
	inner *alloc.Allocator
	seen  map[string]struct{}
}

// New creates an allocator with total capacity C > 0.
func New(c int64) (*Allocator, error) {
	if c <= 0 {
		return nil, ErrInvalidCapacity
	}
	in, _ := alloc.New(c)
	return &Allocator{inner: in, seen: map[string]struct{}{}}, nil
}

// Add validates fully before touching state; a rejected call leaves no trace.
func (a *Allocator) Add(id string, d int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case id == "":
		return ErrEmptyID
	case d < 0:
		return ErrNegativeDemand
	}
	if _, dup := a.seen[id]; dup {
		return ErrDuplicateID
	}
	a.seen[id] = struct{}{}
	a.inner.Add(alloc.Task{ID: id, Demand: d})
	return nil
}

// Allocate returns exact shares for every added task.
func (a *Allocator) Allocate() map[string]mf.Frac {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.inner.Allocate()
}

// SelfCheck verifies the four invariants on C=30, demands 6,12,18,30,0.
func (a *Allocator) SelfCheck() error {
	ts := []alloc.Task{{ID: "A", Demand: 6}, {ID: "B", Demand: 12}, {ID: "C", Demand: 18}, {ID: "D", Demand: 30}, {ID: "Z", Demand: 0}}
	c, _ := New(30)
	for _, t := range ts {
		if err := c.Add(t.ID, t.Demand); err != nil {
			return err
		}
	}
	got, want := c.Allocate(), naive(30, ts)
	for id, f := range want { // inv.1: equals the naive recursive reference
		if got[id].Cmp(f) != 0 {
			return errors.New("selfcheck: mismatch vs naive at " + id)
		}
	}
	if !level(ts, got) { // inv.2: unmet tasks are levelled together
		return errors.New("selfcheck: unmet task below another share")
	}
	if !alloc.SumEquals(got, 30) { // inv.3: exact conservation
		return errors.New("selfcheck: shares do not conserve capacity")
	}
	reject := func(id string, d int64, e error) bool { return errors.Is(c.Add(id, d), e) }
	if !reject("", 1, ErrEmptyID) || !reject("A", 1, ErrDuplicateID) || !reject("Q", -1, ErrNegativeDemand) {
		return errors.New("selfcheck: rejected op accepted or error kind wrong")
	}
	for id, f := range c.Allocate() {
		if f.Cmp(got[id]) != 0 {
			return errors.New("selfcheck: rejected operations changed state")
		}
	}
	return nil
}

// naive is an independent literal transcription of the rule: after each fully
// satisfied task it rescans for the minimum demand and recomputes fair share.
func naive(capacity int64, tasks []alloc.Task) map[string]mf.Frac {
	out := map[string]mf.Frac{}
	var live []alloc.Task
	for _, t := range tasks {
		if t.Demand == 0 {
			out[t.ID] = mf.Int(0)
		} else {
			live = append(live, t)
		}
	}
	var fill func(int64, []alloc.Task)
	fill = func(rem int64, lx []alloc.Task) {
		if len(lx) == 0 {
			return
		}
		fair := mf.Fair(rem, len(lx))
		k := 0
		for i := 1; i < len(lx); i++ {
			if lx[i].Demand < lx[k].Demand {
				k = i
			}
		}
		t := lx[k]
		if !mf.Full(t.Demand, fair) {
			for _, u := range lx {
				out[u.ID] = fair
			}
			return
		}
		out[t.ID] = mf.Int(t.Demand)
		fill(rem-t.Demand, append(append([]alloc.Task{}, lx[:k]...), lx[k+1:]...))
	}
	fill(capacity, live)
	return out
}

// level holds iff no unmet task receives less than any other task.
func level(ts []alloc.Task, got map[string]mf.Frac) bool {
	for _, i := range ts {
		if got[i.ID].Cmp(mf.Int(i.Demand)) >= 0 {
			continue
		}
		for _, j := range ts {
			if got[i.ID].Cmp(got[j.ID]) < 0 {
				return false
			}
		}
	}
	return true
}
