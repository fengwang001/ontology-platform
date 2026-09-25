// Package api is the public facade: build relations R and S, then
// sort-merge join them with external-sort spill.
package api

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/mrg"
	"ontology/run"
)

// Re-exported sentinel errors so callers only import api.
var (
	ErrBadThreshold = run.ErrBadThreshold
	ErrBadFanIn     = mrg.ErrBadFanIn
	ErrNegativeKey  = run.ErrNegativeKey
)

// Key is a tuple key; must be >= 0.
type Key = int

// Pair is one join output tuple pair.
type Pair = mrg.Pair

// Engine holds the two relations and join parameters. Built relations
// are immutable after a successful BuildR/BuildS, so Join and
// SelfCheck are safe for concurrent use.
type Engine struct {
	m, fanIn int
	mu       sync.RWMutex
	runsR    []run.Run
	runsS    []run.Run
}

// New creates an Engine. M must be >= 1, maxFanIn must be >= 2.
func New(M, maxFanIn int) (*Engine, error) {
	if M < 1 {
		return nil, ErrBadThreshold
	}
	if maxFanIn < 2 {
		return nil, ErrBadFanIn
	}
	return &Engine{m: M, fanIn: maxFanIn}, nil
}

// build validates and spills one relation; on error nothing is stored.
func (e *Engine) build(keys []Key, dst *[]run.Run) error {
	runs, err := run.Build(e.m, keys)
	if err != nil {
		return err
	}
	e.mu.Lock()
	*dst = runs
	e.mu.Unlock()
	return nil
}

// BuildR loads relation R. Any negative key rejects the whole batch
// and leaves state unchanged.
func (e *Engine) BuildR(keys []Key) error { return e.build(keys, &e.runsR) }

// BuildS loads relation S. Any negative key rejects the whole batch
// and leaves state unchanged.
func (e *Engine) BuildS(keys []Key) error { return e.build(keys, &e.runsS) }

// Join returns all equal-key pairs as a multiset.
func (e *Engine) Join() ([]Pair, error) {
	e.mu.RLock()
	j, err := mrg.New(e.runsR, e.runsS, e.fanIn)
	e.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	return j.Join(), nil
}

// naive is the nested-loop reference: every R tuple x every S tuple
// with equal keys.
func naive(r, s []Key) []Pair {
	var out []Pair
	for _, a := range r {
		for _, b := range s {
			if a == b {
				out = append(out, Pair{R: a, S: b})
			}
		}
	}
	return out
}

// multisetEqual compares two pair slices as multisets.
func multisetEqual(a, b []Pair) bool {
	if len(a) != len(b) {
		return false
	}
	ca, cb := map[Pair]int{}, map[Pair]int{}
	for _, p := range a {
		ca[p]++
	}
	for _, p := range b {
		cb[p]++
	}
	for p, n := range ca {
		if cb[p] != n {
			return false
		}
	}
	return true
}

// SelfCheck verifies the four invariants on built-in inputs.
func (e *Engine) SelfCheck() error {
	cases := []struct{ r, s []Key }{
		{[]Key{5, 1, 3, 7, 3, 2}, []Key{3, 6, 3, 2}},
		{[]Key{0, 0, 9, 4, 4, 4, 1}, []Key{4, 0, 4, 8}},
		{nil, []Key{1, 2}},
		{[]Key{2, 2}, nil},
	}
	for i, c := range cases {
		eng, err := New(e.m, e.fanIn)
		if err != nil {
			return err
		}
		if err := eng.BuildR(c.r); err != nil {
			return fmt.Errorf("selfcheck case %d BuildR: %w", i, err)
		}
		if err := eng.BuildS(c.s); err != nil {
			return fmt.Errorf("selfcheck case %d BuildS: %w", i, err)
		}
		got, err := eng.Join()
		if err != nil {
			return err
		}
		if !multisetEqual(got, naive(c.r, c.s)) { // invariant 1
			return errors.New("selfcheck: join differs from naive reference")
		}
		eng.mu.RLock()
		runs := [][]run.Run{eng.runsR, eng.runsS}
		eng.mu.RUnlock()
		for _, rs := range runs {
			merged, err := mrg.Sorted(rs, e.fanIn)
			if err != nil {
				return err
			}
			if !sort.IntsAreSorted(merged) { // invariant 2
				return errors.New("selfcheck: merged sequence not ascending")
			}
			for j, rn := range rs { // invariant 3
				bad := len(rn.Keys) > e.m || !sort.IntsAreSorted(rn.Keys) ||
					(j < len(rs)-1 && len(rn.Keys) != e.m)
				if bad {
					return errors.New("selfcheck: run shape violated")
				}
			}
		}
	}
	// invariant 4: rejections leave no trace
	eng, _ := New(2, 2)
	if err := eng.BuildR([]Key{1, -1}); !errors.Is(err, ErrNegativeKey) {
		return errors.New("selfcheck: negative key not rejected")
	}
	if err := eng.BuildS([]Key{5}); err != nil {
		return err
	}
	got, err := eng.Join()
	if err != nil || len(got) != 0 {
		return errors.New("selfcheck: rejected batch left trace")
	}
	return nil
}
