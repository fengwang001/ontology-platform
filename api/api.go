// Package api is the public entry point for micro-batch triggered tumbling-window counting.
package api

import (
	"errors"
	"reflect"
	"sync"

	"ontology/mbatch"
)

type Event = mbatch.Event
type Change = mbatch.Change
type Cell = mbatch.Cell

// Distinct, decidable sentinel errors.
var (
	ErrWNonPositive = mbatch.ErrWNonPositive
	ErrBNonPositive = mbatch.ErrBNonPositive
	ErrEmptyKey     = mbatch.ErrEmptyKey
)

// Counter is the thread-safe public processor.
type Counter struct {
	mu sync.RWMutex
	m  *mbatch.M
}

// New constructs a Counter with window size W and micro-batch capacity B.
func New(W, B int64) (*Counter, error) {
	m, err := mbatch.New(W, B)
	if err != nil {
		return nil, err
	}
	return &Counter{m: m}, nil
}

// Feed appends arrival-ordered events; the slice applies wholly or not at all.
func (c *Counter) Feed(evs []Event) ([]Change, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m.Feed(evs)
}

// Flush seals the trailing partial batch under wm = +infinity.
func (c *Counter) Flush() []Change {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m.Flush()
}

// Emitted returns a copy of every change emitted so far.
func (c *Counter) Emitted() []Change {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.m.Log()
}

// View returns the materialized view after applying every emitted change.
func (c *Counter) View() map[Cell]int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.m.View()
}

// SelfCheck verifies the four invariants on the built-in W=10 B=3 scenario.
func (c *Counter) SelfCheck() error {
	m, err := mbatch.New(10, 3)
	if err != nil {
		return err
	}
	type sig struct {
		s, e, v int64
		p       bool
	}
	proj := func(cs []Change) []sig {
		o := make([]sig, len(cs))
		for i, x := range cs {
			o[i] = sig{x.Win.Start, x.Win.End, x.Value, x.Plus}
		}
		return o
	}
	arrive := func(ts ...int64) []Event {
		e := make([]Event, len(ts))
		for i, t := range ts {
			e[i] = Event{Key: "K", TS: t}
		}
		return e
	}
	want := [][]sig{
		{{0, 10, 1, true}, {10, 20, 1, true}},
		{{0, 10, 1, false}, {0, 10, 2, true}},
		{{20, 30, 3, true}, {0, 10, 2, false}, {0, 10, 3, true}, {10, 20, 1, false}, {10, 20, 2, true}},
		{{30, 40, 1, true}, {0, 10, 3, false}, {0, 10, 4, true}},
	}
	for i, ts := range [][]int64{{5, 12, 20}, {7, 22, 25}, {8, 31, 15}} {
		got, err := m.Feed(arrive(ts...))
		if err != nil || !reflect.DeepEqual(proj(got), want[i]) {
			return errors.New("api: batch emission mismatch")
		}
	}
	if _, err := m.Feed(arrive(9)); err != nil { // tenth event waits in the tail batch
		return err
	}
	if !reflect.DeepEqual(proj(m.Flush()), want[3]) {
		return errors.New("api: flush emission mismatch")
	}
	batch := map[Cell]int64{}
	for _, t := range []int64{5, 12, 20, 7, 22, 25, 8, 31, 15, 9} {
		batch[Cell{Key: "K", K: t / 10}]++
	}
	if view := m.View(); len(view) != len(batch) {
		return errors.New("api: view != batch recomputation")
	} else {
		for id, v := range batch {
			if view[id] != v {
				return errors.New("api: view != batch recomputation")
			}
		}
	}
	cur := map[Cell]int64{}
	for _, ch := range m.Log() {
		id := Cell{Key: ch.Key, K: ch.Win.K}
		if ch.Plus {
			if _, seen := cur[id]; seen {
				return errors.New("api: window + emitted more than once")
			}
			cur[id] = ch.Value
		} else {
			if cur[id] != ch.Value {
				return errors.New("api: minus withdraws a non-current value")
			}
			delete(cur, id)
		}
	}
	for id, v := range batch {
		if cur[id] != v {
			return errors.New("api: changelog tail != batch result")
		}
	}
	return nil
}
