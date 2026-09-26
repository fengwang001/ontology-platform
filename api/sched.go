// Package api is the public entry point of the in-memory resource
// reservation scheduler.
package api

import (
	"errors"
	"fmt"

	"ontology/rsv"
)

// ErrConfig is the fourth, api-owned sentinel: the constructor got capacity<1.
var ErrConfig = errors.New("api: capacity must be at least 1")

// Sched is the resource reservation scheduler.
type Sched struct {
	*rsv.Manager
	capacity int64
}

// New creates a scheduler with total resource capacity (capacity >= 1).
func New(capacity int64) (*Sched, error) {
	if capacity < 1 {
		return nil, ErrConfig
	}
	return &Sched{Manager: rsv.NewManager(capacity), capacity: capacity}, nil
}

// SelfCheck replays built-in operation sequences and verifies the four
// invariants: equivalence with the naive point sweep, capacity never
// exceeded, release invalidates, and rejections leave no trace. It uses only
// local state, so it is safe to call concurrently with normal operations.
func (s *Sched) SelfCheck() error {
	m, err := New(10) // eight-step scenario is prescribed at capacity 10
	if err != nil {
		return err
	}

	// (1) The prescribed eight-step scenario against the naive point sweep.
	type rec struct{ s, e, n, id int64 }
	var act []rec
	naivePeak := func(a, b int64) int64 {
		var peak int64
		for x := a; x < b; x++ {
			var load int64
			for _, r := range act {
				if r.s <= x && x < r.e {
					load += r.n
				}
			}
			if load > peak {
				peak = load
			}
		}
		return peak
	}
	type op struct {
		release bool
		id      int64
		s, e, n int64
	}
	ops := []op{
		{s: 0, e: 5, n: 4}, {s: 5, e: 9, n: 7}, {s: 2, e: 7, n: 5},
		{s: 0, e: 9, n: 1}, {release: true, id: 1}, {s: 2, e: 4, n: 9},
		{s: 0, e: 6, n: 2}, {release: true, id: 2},
	}
	for _, o := range ops {
		if o.release {
			if e := m.Release(o.id); e != nil {
				return fmt.Errorf("selfcheck: release %d: %w", o.id, e)
			}
			k := 0
			for _, r := range act {
				if r.id != o.id {
					act[k] = r
					k++
				}
			}
			act = act[:k]
			continue
		}
		id, ok, e := m.Reserve(o.s, o.e, o.n)
		if e != nil {
			return fmt.Errorf("selfcheck: reserve: %w", e)
		}
		want := naivePeak(o.s, o.e)+o.n <= 10
		if ok != want {
			return fmt.Errorf("selfcheck: [%d,%d) n=%d got ok=%v want %v", o.s, o.e, o.n, ok, want)
		}
		if ok {
			act = append(act, rec{o.s, o.e, o.n, id})
		}
	}

	// (3) Release invalidates: [0,4)5 then release, then [0,4)5 fits again.
	m2, _ := New(s.capacity)
	id, _, _ := m2.Reserve(0, 4, s.capacity)
	if _, ok, _ := m2.Reserve(0, 4, 1); ok {
		return errors.New("selfcheck: expected overload rejection")
	}
	if e := m2.Release(id); e != nil {
		return e
	}
	if _, ok, _ := m2.Reserve(0, 4, 1); !ok {
		return errors.New("selfcheck: release did not free capacity")
	}

	// (4) Rejections leave no trace; (2) accepted load stays within capacity
	// is already enforced by the peak gate exercised in every reserve above.
	before := m2.Active()
	if _, _, e := m2.Reserve(0, 1, 0); !errors.Is(e, rsv.ErrNeed) {
		return errors.New("selfcheck: need sentinel missing")
	}
	if _, _, e := m2.Reserve(1, 0, 1); !errors.Is(e, rsv.ErrInterval) {
		return errors.New("selfcheck: interval sentinel missing")
	}
	if e := m2.Release(1 << 40); !errors.Is(e, rsv.ErrRelease) {
		return errors.New("selfcheck: release sentinel missing")
	}
	if m2.Active() != before {
		return errors.New("selfcheck: rejected operation changed active set")
	}
	return nil
}
