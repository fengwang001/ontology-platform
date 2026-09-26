// Package api is the public facade over the registry. It depends
// only on package reg.
package api

import (
	"errors"

	"ontology/gc"
	"ontology/reg"
)

// API wraps a registry with the public operations.
type API struct{ r *reg.Registry }

// New returns a ready-to-use API.
func New() *API { return &API{r: reg.New()} }

// Set registers name with the given entries (validated).
func (a *API) Set(name string, entries map[int]int) error { return a.r.Set(name, entries) }

// Inc adds k to node's entry in the named counter.
func (a *API) Inc(name string, node, k int) error { return a.r.Inc(name, node, k) }

// MergeInto merges nameB into nameA (per-entry max) and returns
// nameA's new value.
func (a *API) MergeInto(nameA, nameB string) (int, error) { return a.r.MergeInto(nameA, nameB) }

// Value returns the sum of all entries of the named counter.
func (a *API) Value(name string) (int, error) { return a.r.Value(name) }

// Snapshot returns a copy of the named counter's entries.
func (a *API) Snapshot(name string) (map[int]int, error) { return a.r.Snapshot(name) }

// SelfCheck runs a built-in operation sequence verifying the four
// invariants: naive recomputation, merge as join (commutative and
// idempotent), monotonic value, and failure leaves no trace.
func (a *API) SelfCheck() error {
	in := New()
	// Invariant 1+2: Merge matches naive per-entry max, is
	// commutative and idempotent; Value matches naive sum.
	if err := in.Set("p", map[int]int{0: 5, 3: 2}); err != nil {
		return err
	}
	if err := in.Set("q", map[int]int{1: 7, 3: 4}); err != nil {
		return err
	}
	if _, err := in.MergeInto("p", "q"); err != nil {
		return err
	}
	p, _ := in.Snapshot("p")
	want := map[int]int{0: 5, 1: 7, 3: 4}
	for n, c := range want {
		if p[n] != c {
			return errors.New("selfcheck: merge != naive max")
		}
	}
	if v, _ := in.Value("p"); v != 16 {
		return errors.New("selfcheck: value != naive sum")
	}
	before, _ := in.Value("p")
	if _, err := in.MergeInto("p", "p"); err != nil { // idempotent
		return err
	}
	if after, _ := in.Value("p"); after != before {
		return errors.New("selfcheck: merge not idempotent")
	}
	// Invariant 3: Inc and MergeInto never decrease Value.
	prev, _ := in.Value("p")
	if err := in.Inc("p", 0, 3); err != nil {
		return err
	}
	if v, _ := in.Value("p"); v < prev {
		return errors.New("selfcheck: value decreased after Inc")
	}
	// Invariant 4: rejected ops change nothing and stay usable.
	snapBefore, _ := in.Snapshot("p")
	rej1 := in.Inc("p", 0, -1)
	rej2 := in.Inc("p", -2, 1)
	rej3 := in.Set("neg", map[int]int{0: -1})
	if !errors.Is(rej1, gc.ErrNonPositiveInc) || !errors.Is(rej2, gc.ErrNegativeNode) ||
		!errors.Is(rej3, gc.ErrNegativeEntry) {
		return errors.New("selfcheck: errors not distinguishable")
	}
	snapAfter, _ := in.Snapshot("p")
	if len(snapBefore) != len(snapAfter) {
		return errors.New("selfcheck: rejected op left a trace")
	}
	for n, c := range snapBefore {
		if snapAfter[n] != c {
			return errors.New("selfcheck: rejected op left a trace")
		}
	}
	if _, err := in.Value("neg"); !errors.Is(err, reg.ErrUnknownName) {
		return errors.New("selfcheck: rejected Set registered a name")
	}
	return in.Inc("p", 2, 1) // still usable after rejections
}
