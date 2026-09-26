// Package replica maintains the in-memory versioned map and decides, for each
// incoming delta, whether it is a duplicate, an out-of-order gap, or ready to
// apply. It depends only on the delta package.
package replica

import (
	"errors"
	"fmt"
	"sync"

	"ontology/delta"
)

// Sentinel errors are pairwise distinct so callers can judge the failure kind.
var (
	ErrGap             = errors.New("replica: delta starts at a future version (gap)")
	ErrInvalidRange    = errors.New("replica: delta To must be strictly greater than From")
	ErrNegativeVersion = errors.New("replica: delta versions must not be negative")
)

// Replica is the in-memory versioned key/value state.
type Replica struct {
	mu      sync.RWMutex
	version int
	state   map[string]int
	// probeCount: applied-version records read by the most recent Apply while
	// deciding duplicate vs gap. The O(1) design compares From against this
	// single current version, so it is always 1. Unexported on purpose.
	probeCount int
}

// New returns a replica at version 0 with an empty map.
func New() *Replica { return &Replica{state: map[string]int{}} }

// Version returns the current version (monotonic, concurrent-safe).
func (r *Replica) Version() int {
	r.mu.RLock()
	v := r.version
	r.mu.RUnlock()
	return v
}

// State returns a copy of the current map (values are ints).
func (r *Replica) State() map[string]int {
	r.mu.RLock()
	out := make(map[string]int, len(r.state))
	for k, v := range r.state {
		out[k] = v
	}
	r.mu.RUnlock()
	return out
}

// Apply feeds one delta. Malformed deltas (negative versions, bad range, empty
// key) are rejected before the version comparison; a gap is rejected; a
// duplicate is skipped idempotently; an in-order delta is applied then advanced.
func (r *Replica) Apply(d delta.Delta) error {
	if d.From < 0 || d.To < 0 {
		return ErrNegativeVersion
	}
	if d.To <= d.From {
		return ErrInvalidRange
	}
	for _, c := range d.Changes { // validate the whole batch before any decision
		if c.Key == "" {
			return delta.ErrEmptyKey
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.probeCount = 1 // exactly one applied-version record (the current version) is read
	switch {
	case d.From > r.version:
		return ErrGap
	case d.From < r.version:
		return nil // already applied: idempotent skip, state and version untouched
	}
	for _, c := range d.Changes { // apply in given order: set overwrites, del removes
		if err := delta.ApplyChange(r.state, c); err != nil {
			return err
		}
	}
	r.version = d.To
	return nil
}

// SelfCheck replays a built-in sequence and verifies the four invariants plus
// the O(1) probe bound. It returns nil only when every check passes; the probe
// count value itself never leaves the package.
func (r *Replica) SelfCheck() error {
	seq := []delta.Delta{
		{From: 0, To: 1, Changes: []delta.Change{delta.Set("a", 1)}},
		{From: 1, To: 2, Changes: []delta.Change{delta.Set("b", 2)}},
		{From: 2, To: 3, Changes: []delta.Change{delta.Set("c", 3), delta.Del("b")}},
		{From: 3, To: 4, Changes: []delta.Change{delta.Set("d", 4)}},
		{From: 0, To: 1, Changes: []delta.Change{delta.Set("a", 100)}},
	}
	want := []map[string]int{{"a": 1}, {"a": 1, "b": 2}, {"a": 1, "c": 3},
		{"a": 1, "c": 3, "d": 4}, {"a": 1, "c": 3, "d": 4}}
	wantVer := []int{1, 2, 3, 4, 4}
	fresh := New()
	prev := 0
	for i, d := range seq {
		if err := fresh.Apply(d); err != nil {
			return fmt.Errorf("selfcheck: apply %d: %w", i+1, err)
		}
		if fresh.Version() < prev || fresh.Version() != wantVer[i] || !equalMap(fresh.State(), want[i]) {
			return fmt.Errorf("selfcheck: mismatch at %d: v=%d %v", i+1, fresh.Version(), fresh.State())
		}
		prev = fresh.Version()
	}
	before, bv := fresh.State(), fresh.Version()
	for _, d := range []delta.Delta{seq[0], seq[2], seq[4]} { // duplicates leave no trace
		if err := fresh.Apply(d); err != nil || fresh.Version() != bv || !equalMap(fresh.State(), before) {
			return errors.New("selfcheck: duplicate not idempotent")
		}
	}
	bad := []delta.Delta{{From: 9, To: 10}, {From: 0, To: 0}, {From: -1, To: 1},
		{From: 0, To: 1, Changes: []delta.Change{delta.Set("", 1)}}}
	for _, d := range bad { // every rejection is whole-failure with no trace
		if fresh.Apply(d) == nil || fresh.Version() != bv || !equalMap(fresh.State(), before) {
			return errors.New("selfcheck: rejected delta left a trace")
		}
	}
	for _, m := range []int{100, 1000, 10000} { // O(1): one version record read regardless of m
		o := New()
		for i := 0; i < m; i++ {
			if err := o.Apply(delta.Delta{From: i, To: i + 1, Changes: []delta.Change{delta.Set("k", i)}}); err != nil {
				return err
			}
		}
		if err := o.Apply(delta.Delta{From: m, To: m + 1}); err != nil || o.probeCount != 1 {
			return fmt.Errorf("selfcheck: probe not O(1) at m=%d (count=%d)", m, o.probeCount)
		}
	}
	return nil
}

func equalMap(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
