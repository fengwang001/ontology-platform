// Package gk is the Greenwald-Khanna quantile summary core: the tuple
// list, INSERT, COMPRESS and rmin/rmax arithmetic. It depends on nothing.
//
// Merge keeps the LARGER v (standard GK); the spec's literal "smaller v
// survives" wording makes rmin cease to be a lower bound on rank and
// leaves query error unbounded. See NOTES.md.
package gk

import (
	"fmt"
	"slices"
	"sort"
)

// Tuple is one (v, g, Δ) entry of the summary.
type Tuple struct {
	V int64
	G int64
	D int64 // Δ
}

// Summary is an ordered list of tuples plus the observation count n.
type Summary struct {
	eps float64
	t   []Tuple
	n   int64
}

// New returns an empty summary for precision eps (0 < eps < 1).
func New(eps float64) *Summary { return &Summary{eps: eps} }

// N returns the number of inserted observations.
func (s *Summary) N() int64 { return s.n }

// Eps returns the precision parameter.
func (s *Summary) Eps() float64 { return s.eps }

// Tuples returns a copy of the tuple list, v ascending.
func (s *Summary) Tuples() []Tuple { return slices.Clone(s.t) }

// Insert adds v, which must differ from every previously inserted value.
// Head/tail inserts get Δ=0; a middle insert gets Δ = g_i+Δ_i−1 of the
// first tuple with v_i > v.
func (s *Summary) Insert(v int64) {
	s.n++
	switch {
	case len(s.t) == 0 || v < s.t[0].V:
		s.t = slices.Insert(s.t, 0, Tuple{V: v, G: 1})
	case v > s.t[len(s.t)-1].V:
		s.t = append(s.t, Tuple{V: v, G: 1})
	default:
		i := sort.Search(len(s.t), func(i int) bool { return s.t[i].V > v })
		s.t = slices.Insert(s.t, i, Tuple{V: v, G: 1, D: s.t[i].G + s.t[i].D - 1})
	}
}

// Compress merges adjacent tuples back to front while
// g_{i-1}+g_i+Δ_i ≤ 2εn, per the spec rule.
func (s *Summary) Compress() { s.CompressBelow(2 * s.eps * float64(s.n)) }

// CompressBelow is Compress with an explicit band limit; the api package
// uses a tighter limit so the query error stays within ε·n (NOTES.md).
func (s *Summary) CompressBelow(lim float64) {
	for i := len(s.t) - 1; i >= 1; i-- {
		if float64(s.t[i-1].G+s.t[i].G+s.t[i].D) <= lim {
			s.t[i].G += s.t[i-1].G
			s.t = slices.Delete(s.t, i-1, i)
		}
	}
}

// BandOK reports the band invariant g_i+Δ_i ≤ 2εn for every tuple.
// The limit is floored at 1 because g ≥ 1 always (else the spec's own
// first insert would violate it).
func (s *Summary) BandOK() bool {
	lim := 2 * s.eps * float64(s.n)
	if lim < 1 {
		lim = 1
	}
	for _, e := range s.t {
		if float64(e.G+e.D) > lim {
			return false
		}
	}
	return true
}

// String renders the summary as [(v,g,Δ),...] for the demo trace.
func (s *Summary) String() string {
	out := "["
	for i, e := range s.t {
		if i > 0 {
			out += ","
		}
		out += fmt.Sprintf("(%d,%d,%d)", e.V, e.G, e.D)
	}
	return out + "]"
}
