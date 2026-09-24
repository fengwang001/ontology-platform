// Package rel represents sets of binary tuples (x, y) and their operations.
// It depends on nothing outside the standard library.
package rel

import "sort"

// Pair is a binary tuple (X, Y) over string nodes.
type Pair struct{ X, Y string }

// Set is a collection of distinct Pairs (set semantics).
type Set struct{ m map[Pair]struct{} }

// New returns an empty Set.
func New() *Set { return &Set{m: map[Pair]struct{}{}} }

// Add inserts p and reports whether it was not already present.
func (s *Set) Add(p Pair) bool {
	if _, ok := s.m[p]; ok {
		return false
	}
	s.m[p] = struct{}{}
	return true
}

// Has reports whether p is in the set.
func (s *Set) Has(p Pair) bool { _, ok := s.m[p]; return ok }

// Size returns the number of tuples in the set.
func (s *Set) Size() int { return len(s.m) }

// Union adds every tuple of o into s.
func (s *Set) Union(o *Set) {
	for p := range o.m {
		s.m[p] = struct{}{}
	}
}

// Diff returns a new set with the tuples of s that are not in o.
func (s *Set) Diff(o *Set) *Set {
	d := New()
	for p := range s.m {
		if !o.Has(p) {
			d.m[p] = struct{}{}
		}
	}
	return d
}

// Pairs returns all tuples in deterministic (X, then Y) sorted order.
func (s *Set) Pairs() []Pair {
	ps := make([]Pair, 0, len(s.m))
	for p := range s.m {
		ps = append(ps, p)
	}
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].X != ps[j].X {
			return ps[i].X < ps[j].X
		}
		return ps[i].Y < ps[j].Y
	})
	return ps
}
