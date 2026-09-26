// Package gk implements the Greenwald–Khanna ε-approximate quantile
// summary: the tuple list, INSERT, COMPRESS, and rmin/rmax support.
// It depends on nothing outside the standard library.
package gk

import (
	"errors"
	"slices"
	"sort"
)

// ErrDuplicate reports an INSERT of a value already present.
var ErrDuplicate = errors.New("gk: duplicate value")

// Tuple is one (v, g, Δ) entry of the summary.
type Tuple struct {
	V    int64
	G, D int64
}

// Summary is a GK quantile summary: tuples sorted by V, no duplicates.
type Summary struct {
	eps    float64
	n      int64
	tuples []Tuple
	seen   map[int64]struct{}
	lo, hi int64 // true min/max inserted values
}

// New returns an empty summary for precision eps (0 < eps < 1).
func New(eps float64) *Summary {
	return &Summary{eps: eps, seen: make(map[int64]struct{})}
}

// N reports how many values have been inserted.
func (s *Summary) N() int64 { return s.n }

// Eps returns the precision parameter.
func (s *Summary) Eps() float64 { return s.eps }

// Len returns the number of tuples currently held.
func (s *Summary) Len() int { return len(s.tuples) }

// Tuples returns a copy of the tuple list, sorted by V.
func (s *Summary) Tuples() []Tuple { return slices.Clone(s.tuples) }

// band is max(1, ⌊2εn⌋): the bound every g+Δ must stay within.
// The floor of 1 covers n < 1/2ε, where 2εn < 1 ≤ g.
func (s *Summary) band() int64 {
	if b := int64(2 * s.eps * float64(s.n)); b > 1 {
		return b
	}
	return 1
}

// Insert adds v, which must differ from every previously inserted value.
// A rejected insert (ErrDuplicate) leaves the summary untouched.
//
// Δ = 0 only for a true extreme (v below every / above every inserted
// value); otherwise Δ = g_i + Δ_i − 1 of the first tuple with v_i > v
// (the last tuple if v exceeds every representative but not hi).
func (s *Summary) Insert(v int64) error {
	if _, ok := s.seen[v]; ok {
		return ErrDuplicate
	}
	t := Tuple{V: v, G: 1}
	i := sort.Search(len(s.tuples), func(i int) bool { return s.tuples[i].V > v })
	switch {
	case s.n == 0:
		s.lo, s.hi = v, v
	case v < s.lo:
		s.lo, i = v, 0
	case v > s.hi:
		s.hi, i = v, len(s.tuples)
	default:
		j := i
		if j == len(s.tuples) { // lo < v < hi but beyond last representative:
			j-- // borrow the last tuple's g_i + Δ_i − 1, then append
		}
		t.D = s.tuples[j].G + s.tuples[j].D - 1
	}
	s.tuples = slices.Insert(s.tuples, i, t)
	s.seen[v] = struct{}{}
	s.n++
	return nil
}

// Compress merges adjacent tuples back to front while
// g_{i-1} + g_i + Δ_i ≤ 2εn.
func (s *Summary) Compress() {
	for i := len(s.tuples) - 1; i > 0; i-- {
		if s.tuples[i-1].G+s.tuples[i].G+s.tuples[i].D <= s.band() {
			s.tuples[i-1].G += s.tuples[i].G
			s.tuples[i-1].D = s.tuples[i].D
			s.tuples = slices.Delete(s.tuples, i, i+1)
		}
	}
}

// BandOK reports whether every tuple satisfies g+Δ ≤ max(1, 2εn).
func (s *Summary) BandOK() bool {
	for _, t := range s.tuples {
		if t.G+t.D > s.band() {
			return false
		}
	}
	return true
}
