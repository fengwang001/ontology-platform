// Package samp computes the indices of a systematic (equal-interval) sample
// from a sorted population of size n.
//
// It depends on no other package. Given sample size s (1 <= s <= n) and
// offset r (0 <= r < n/s), the interval is d = n/s (a real number) and the
// i-th index is floor(r + i*d) for i = 0 .. s-1.
package samp

import (
	"errors"
	"math"
)

// Sentinel errors. Both are fail-closed: New returns nil together with the
// error, so no state is created by a rejected call.
var (
	// ErrInvalidSize reports s <= 0 or s > n.
	ErrInvalidSize = errors.New("samp: invalid sample size: require 1 <= s <= N")
	// ErrOffsetOutOfRange reports r < 0 or r >= n/s.
	ErrOffsetOutOfRange = errors.New("samp: offset out of range: require 0 <= r < N/s")
)

// Plan is an immutable set of computed sample indices.
type Plan struct {
	n   int
	s   int
	d   float64
	r   float64
	idx []int
}

// New validates the parameters and, when valid, computes exactly s indices.
// It fails before constructing anything when s or r is illegal, so a
// rejected call leaves no object behind.
func New(n, s int, r float64) (*Plan, error) {
	if s <= 0 || s > n {
		return nil, ErrInvalidSize
	}
	d := float64(n) / float64(s)
	if math.IsNaN(r) || math.IsInf(r, 0) || r < 0 || r >= d {
		return nil, ErrOffsetOutOfRange
	}
	idx := make([]int, s)
	for i := 0; i < s; i++ {
		idx[i] = int(math.Floor(r + float64(i)*d))
	}
	return &Plan{n: n, s: s, d: d, r: r, idx: idx}, nil
}

// Indices returns a copy of the s computed indices. Returning a copy keeps
// the plan immutable and lets callers share it across goroutines.
func (p *Plan) Indices() []int {
	out := make([]int, len(p.idx))
	copy(out, p.idx)
	return out
}

// N reports the population size the plan was built for.
func (p *Plan) N() int { return p.n }

// Size reports the sample size s.
func (p *Plan) Size() int { return p.s }
