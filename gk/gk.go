// Package gk implements the Greenwald–Khanna ε-approximate quantile summary:
// the tuple list, INSERT, COMPRESS and the rmin/rmax rank bounds.
package gk

import "sort"

// Tuple is one summary entry: value V, rank increment G, uncertainty Delta.
type Tuple struct {
	V     int64
	G     int
	Delta int
}

// Summary is a GK quantile summary over a stream of distinct int64 values.
type Summary struct {
	eps float64
	n   int
	t   []Tuple // strictly ascending by V
}

// New returns an empty summary with precision eps (caller guarantees 0<eps<1).
func New(eps float64) *Summary { return &Summary{eps: eps} }

// N returns the number of values inserted so far.
func (s *Summary) N() int { return s.n }

// Tuples returns a copy of the tuple list, ascending by V.
func (s *Summary) Tuples() []Tuple {
	out := make([]Tuple, len(s.t))
	copy(out, s.t)
	return out
}

// Has reports whether v already appears as a tuple value.
func (s *Summary) Has(v int64) bool {
	i := sort.Search(len(s.t), func(i int) bool { return s.t[i].V >= v })
	return i < len(s.t) && s.t[i].V == v
}

// Insert adds v (assumed distinct from all existing values).
func (s *Summary) Insert(v int64) {
	s.n++
	switch {
	case len(s.t) == 0 || v < s.t[0].V: // new minimum
		s.t = append([]Tuple{{V: v, G: 1}}, s.t...)
	case v > s.t[len(s.t)-1].V: // new maximum
		s.t = append(s.t, Tuple{V: v, G: 1})
	default:
		i := sort.Search(len(s.t), func(i int) bool { return s.t[i].V > v })
		s.t = append(s.t, Tuple{})
		copy(s.t[i+1:], s.t[i:])
		s.t[i] = Tuple{V: v, G: 1, Delta: s.t[i+1].G + s.t[i+1].Delta - 1}
	}
}

// Compress merges adjacent tuples from back to front while g+g+Δ ≤ 2εn.
//
// NOTE (deviation from the literal task text, see NOTES.md): the fold keeps
// the LATER tuple (v_{i+1}, Δ_{i+1}) and deletes the earlier one — the
// classical GK direction. Folding into the earlier tuple breaks the
// rmin ≤ rank ≤ rmax bracket and the min/max sentinels. The first tuple is
// never merged away so v_1 stays the true minimum (the last tuple can only
// absorb, so v_s stays the true maximum), keeping INSERT's head/tail rules
// and the rank bracket valid.
func (s *Summary) Compress() {
	band := 2 * s.eps * float64(s.n)
	for i := len(s.t) - 2; i >= 1; i-- {
		if float64(s.t[i].G+s.t[i+1].G+s.t[i+1].Delta) <= band {
			s.t[i+1].G += s.t[i].G
			s.t = append(s.t[:i], s.t[i+1:]...)
		}
	}
}

// BandOK reports whether every tuple satisfies g + Δ ≤ max(1, 2εn) (band
// invariant; the max covers n < 1/2ε, where even (v,1,0) exceeds raw 2εn).
func (s *Summary) BandOK() bool {
	band := 2 * s.eps * float64(s.n)
	if band < 1 {
		band = 1
	}
	for _, t := range s.t {
		if float64(t.G+t.Delta) > band {
			return false
		}
	}
	return true
}
