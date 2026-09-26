// Package ewma implements a single-series exponentially weighted
// moving average with O(1) memory. It depends on no other package.
package ewma

import (
	"math"
	"sync/atomic"
)

// EWMA keeps the running state of one time series: the current
// smoothed value s, the seed, the observation count n and the
// smoothing factor alpha.
type EWMA struct {
	alpha       float64
	seed        float64
	biasCorrect bool
	s           float64
	n           int
	// lastReads records how many historical observations the most
	// recent Value call read to produce its result. Unexported on
	// purpose: it must never leak through the public API. Atomic
	// because concurrent Value calls may record it simultaneously.
	lastReads atomic.Int32
}

// New returns an EWMA with smoothing factor alpha (0<alpha<1),
// initial mean seed and optional bias correction.
func New(alpha, seed float64, biasCorrect bool) *EWMA {
	return &EWMA{alpha: alpha, seed: seed, biasCorrect: biasCorrect, s: seed}
}

// Update folds one observation x into the mean:
// s <- alpha*x + (1-alpha)*s_old, and increments the count.
func (e *EWMA) Update(x float64) {
	e.s = e.alpha*x + (1-e.alpha)*e.s
	e.n++
}

// Value returns the current mean. With bias correction enabled and
// n>0 it returns s/(1-(1-alpha)^n); with n==0 it returns the seed.
// It reads exactly one piece of state (s), never the history.
func (e *EWMA) Value() float64 {
	e.lastReads.Store(1)
	if e.biasCorrect && e.n > 0 {
		return e.s / (1 - math.Pow(1-e.alpha, float64(e.n)))
	}
	return e.s
}

// Count returns the number of observations folded in so far.
func (e *EWMA) Count() int { return e.n }
