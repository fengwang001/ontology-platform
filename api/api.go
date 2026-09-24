// Package api is the public facade of the counting bloom filter.
package api

import (
	"errors"
	"fmt"

	"ontology/cbf"
	"ontology/hash"
)

// Sentinel errors re-exported for callers; mutually distinguishable.
var (
	ErrInvalidParams = hash.ErrInvalidParams
	ErrOverflow      = cbf.ErrOverflow
	ErrNotInserted   = cbf.ErrNotInserted
)

// Filter wraps cbf.Filter.
type Filter struct{ f *cbf.Filter }

// New builds a filter; invalid parameters yield ErrInvalidParams.
func New(m, k int, maxCount uint8) (*Filter, error) {
	f, err := cbf.New(m, k, maxCount)
	if err != nil {
		return nil, err
	}
	return &Filter{f: f}, nil
}

// Insert adds x; ErrOverflow rejects the whole insert atomically.
// Delete removes one insertion of x; ErrNotInserted if never inserted.
// Contains reports probable membership; never a false negative.
func (a *Filter) Insert(x int64) error { return a.f.Insert(x) }

func (a *Filter) Delete(x int64) error { return a.f.Delete(x) }

func (a *Filter) Contains(x int64) bool { return a.f.Contains(x) }

// Sum returns the total of all counters; Snapshot copies the counter array.
func (a *Filter) Sum() int { return a.f.Sum() }

func (a *Filter) Snapshot() []uint8 { return a.f.Snapshot() }

// SelfCheck replays built-in sequences verifying the four invariants: no
// false negatives, conservation, naive-model agreement, atomic failures.
func SelfCheck() error {
	const m, k, maxC = 101, 5, 255
	f, err := New(m, k, maxC)
	if err != nil {
		return err
	}
	model := map[int64]int{} // exact multiset of live insertions
	net := 0
	apply := func(x int64, del bool) error {
		var err error
		if del {
			err = f.Delete(x)
		} else {
			err = f.Insert(x)
		}
		if err != nil {
			return err
		}
		if del {
			model[x]--
			if model[x] == 0 {
				delete(model, x)
			}
			net--
		} else {
			model[x]++
			net++
		}
		// Invariant 2: counter conservation.
		if f.Sum() != k*net {
			return fmt.Errorf("conservation broken: sum=%d want=%d", f.Sum(), k*net)
		}
		return nil
	}
	for x := int64(0); x < 40; x++ {
		if err := apply(x, false); err != nil {
			return fmt.Errorf("insert %d: %w", x, err)
		}
	}
	for x := int64(0); x < 40; x += 2 {
		if err := apply(x, true); err != nil {
			return fmt.Errorf("delete %d: %w", x, err)
		}
	}
	// Invariant 1: no false negatives for live keys.
	for x := range model {
		if !f.Contains(x) {
			return fmt.Errorf("false negative for %d", x)
		}
	}
	// Invariant 3: agree with naive recomputation from the exact multiset.
	naive := func(x int64) bool {
		for _, p := range hash.Positions(x, m, k) {
			n := 0
			for key, c := range model {
				for _, q := range hash.Positions(key, m, k) {
					if q == p {
						n += c
					}
				}
			}
			if n == 0 {
				return false
			}
		}
		return true
	}
	for x := int64(0); x < 80; x++ {
		if f.Contains(x) != naive(x) {
			return fmt.Errorf("naive mismatch at %d", x)
		}
	}
	// Invariant 4: rejected operations leave no trace.
	if _, err := New(100, k, maxC); !errors.Is(err, ErrInvalidParams) {
		return fmt.Errorf("bad params: %v", err)
	}
	before := f.Snapshot()
	g, _ := New(7, 3, 2)
	_ = g.Insert(3)
	_ = g.Insert(3)
	gBefore := g.Snapshot()
	if err := g.Insert(3); !errors.Is(err, ErrOverflow) {
		return fmt.Errorf("overflow: %v", err)
	}
	if err := g.Delete(2); !errors.Is(err, ErrNotInserted) {
		return fmt.Errorf("not-inserted: %v", err)
	}
	if !equal(g.Snapshot(), gBefore) || !equal(f.Snapshot(), before) {
		return errors.New("rejected op mutated state")
	}
	return nil
}

func equal(a, b []uint8) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
