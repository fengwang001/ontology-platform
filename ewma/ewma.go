// Package ewma maintains the exponentially weighted moving average of a
// single observation series with O(1) memory: only the current state s,
// the fixed seed and the observation count are retained.
package ewma

import (
	"errors"
	"sync/atomic"
)

// ErrInvalidAlpha is returned when the smoothing factor is outside (0,1).
var ErrInvalidAlpha = errors.New("ewma: alpha must satisfy 0 < alpha < 1")

// EWMA is one independent exponential moving average.
type EWMA struct {
	alpha       float64
	beta        float64 // 1-alpha
	seed        float64
	s           float64 // current mean; equals seed before the first Update
	n           uint64  // observation count
	betaPow     float64 // beta**n, maintained incrementally
	biasCorrect bool

	// reads is the number of historical observations the most recent
	// Value call read to produce its result. The recurrence keeps only
	// the single aggregated state s, so every Value reads exactly one.
	// Unexported on purpose: it must never appear in the public API;
	// atomic so concurrent Value calls (api holds only an RLock) race-free.
	reads atomic.Int64
}

// New returns an EWMA with smoothing factor alpha, initial mean seed and
// optional bias correction.
func New(alpha, seed float64, biasCorrect bool) (*EWMA, error) {
	if alpha <= 0 || alpha >= 1 {
		return nil, ErrInvalidAlpha
	}
	return &EWMA{
		alpha:       alpha,
		beta:        1 - alpha,
		seed:        seed,
		s:           seed,
		betaPow:     1,
		biasCorrect: biasCorrect,
	}, nil
}

// Update folds one observation into the mean:
// s ← alpha*x + (1-alpha)*s_old and increments the count.
func (e *EWMA) Update(x float64) {
	e.s = e.alpha*x + e.beta*e.s
	e.betaPow *= e.beta
	e.n++
}

// Value returns the mean. With bias correction it returns
// s/(1-(1-alpha)^n); with no observations it returns seed.
func (e *EWMA) Value() float64 {
	// Producing the answer reads only the single aggregated state s:
	// no historical observation is ever revisited.
	e.reads.Store(1)
	if e.n == 0 {
		return e.seed
	}
	if !e.biasCorrect {
		return e.s
	}
	return e.s / (1 - e.betaPow)
}

// Count returns the number of observations folded in so far.
func (e *EWMA) Count() uint64 { return e.n }
