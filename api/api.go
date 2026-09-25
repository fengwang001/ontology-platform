// Package api is the public surface of the index nested-loop join.
// Depends on inj (and idx for error sentinels and SelfCheck).
package api

import (
	"fmt"

	"ontology/idx"
	"ontology/inj"
)

// Key is a tuple key; keys must be >= 0.
type Key = int

// Pair is one join result tuple (r, s) with r.Key == s.Key.
type Pair = inj.Pair

// Distinguishable sentinel errors, re-exported for callers.
var (
	ErrNilInput    = idx.ErrNilInput
	ErrNegativeKey = idx.ErrNegativeKey
	ErrNotSorted   = idx.ErrNotSorted
	ErrNoIndex     = inj.ErrNoIndex
)

// Session is one join instance: an inner-relation index plus probes.
type Session struct{ j *inj.Joiner }

// New returns an empty session.
func New() *Session { return &Session{j: inj.NewJoiner()} }

// BuildIndex validates s (non-nil, >= 0 keys, ascending) and installs it
// as the inner relation. Any rejection fails the whole batch and leaves
// the previous state untouched.
func (s *Session) BuildIndex(keys []Key) error {
	x, err := idx.Build(keys) // fully validated before any state changes
	if err != nil {
		return err
	}
	s.j.SetIndex(x)
	return nil
}

// Probe scans r in order and returns all pairs (r, s) with equal keys.
func (s *Session) Probe(r []Key) ([]Pair, error) { return s.j.Probe(r) }

// SelfCheck verifies the four invariants on built-in inputs, plus the
// logarithmic probe cost. It touches no session state, so it is safe to
// call concurrently with anything.
func SelfCheck() error {
	s := []int{2, 3, 3, 3, 5, 7}
	r := []int{3, 5, 1, 3}
	x, err := idx.Build(s)
	if err != nil {
		return fmt.Errorf("selfcheck build: %w", err)
	}
	// Invariant 3: index is ascending and the same multiset as S.
	if x.Len() != len(s) {
		return fmt.Errorf("selfcheck: index len %d, want %d", x.Len(), len(s))
	}
	for i := 0; i < x.Len(); i++ {
		if x.At(i) != s[i] { // s is ascending: its sorted copy must equal it
			return fmt.Errorf("selfcheck: index[%d]=%d, want %d", i, x.At(i), s[i])
		}
	}
	// Invariant 2: [lo, hi) holds exactly the tuples equal to k.
	for _, k := range []int{1, 2, 3, 5, 7, 8} {
		lo, hi := x.Bounds(k, nil)
		for i := 0; i < x.Len(); i++ {
			if (x.At(i) == k) != (lo <= i && i < hi) {
				return fmt.Errorf("selfcheck: interval [%d,%d) wrong for key %d", lo, hi, k)
			}
		}
	}
	// Invariant 1: join output equals the naive nested loop (multiset).
	sess := New()
	if err := sess.BuildIndex(s); err != nil {
		return err
	}
	got, err := sess.Probe(r)
	if err != nil {
		return err
	}
	if !sameMultiset(got, naive(s, r)) {
		return fmt.Errorf("selfcheck: join %v differs from naive", got)
	}
	// Invariant 4: rejected operations leave no trace.
	for _, bad := range [][]int{nil, {-1}, {2, 1}} {
		if err := sess.BuildIndex(bad); err == nil {
			return fmt.Errorf("selfcheck: bad BuildIndex %v accepted", bad)
		}
	}
	for _, bad := range [][]int{nil, {-3}} {
		if _, err := sess.Probe(bad); err == nil {
			return fmt.Errorf("selfcheck: bad Probe %v accepted", bad)
		}
	}
	again, err := sess.Probe(r)
	if err != nil || !sameMultiset(got, again) {
		return fmt.Errorf("selfcheck: state changed by rejected ops")
	}
	return inj.CheckCost()
}

// naive is the reference nested loop: every r x every s, paired on equal keys.
func naive(s, r []int) []Pair {
	var out []Pair
	for _, rk := range r {
		for _, sk := range s {
			if rk == sk {
				out = append(out, Pair{R: rk, S: sk})
			}
		}
	}
	return out
}

func sameMultiset(a, b []Pair) bool {
	if len(a) != len(b) {
		return false
	}
	used := make([]bool, len(b))
	for _, p := range a {
		found := false
		for i, q := range b {
			if !used[i] && p == q {
				used[i], found = true, true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
