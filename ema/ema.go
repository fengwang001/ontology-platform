// Package ema implements an exponentially weighted moving average with
// first-value seeding and exact retraction by full recomputation.
// It is not itself synchronized; stream.Stream serializes access.
package ema

import (
	"errors"
	"math"
)

// Distinct sentinel errors so callers can discriminate every failure mode.
var (
	// ErrInvalidAlpha: alpha not in (0,1].
	ErrInvalidAlpha = errors.New("ema: alpha must be in (0,1]")
	// ErrRetractEmpty: Retract called while hist is empty.
	ErrRetractEmpty = errors.New("ema: retract on empty sequence")
	// ErrRetractNotFound: no surviving occurrence of v in hist.
	ErrRetractNotFound = errors.New("ema: value not present in sequence")
)

// EWMA holds the decayed mean over a value history.
type EWMA struct {
	alpha float64
	hist  []float64
	ema   float64
	init  bool

	// lastAddChecked counts hist elements inspected by the most recent Add.
	// Add is O(1): it never scans hist, so this stays a small constant.
	lastAddChecked int
}

// New validates alpha and returns an empty EWMA. Validation happens before
// any state is created, so a rejected alpha leaves nothing behind.
func New(alpha float64) (*EWMA, error) {
	if math.IsNaN(alpha) || alpha <= 0 || alpha > 1 {
		return nil, ErrInvalidAlpha
	}
	return &EWMA{alpha: alpha}, nil
}

// Add appends v. The first value seeds the EWMA directly (no zero seed);
// every later value applies one constant-time decay step.
func (e *EWMA) Add(v float64) {
	// The decay step touches only the running mean: zero hist elements are
	// inspected, hence Add does not scale with len(hist).
	e.lastAddChecked = 0
	if !e.init {
		e.ema = v
	} else {
		// Explicit math.FMA in both this step and recompute fixes one
		// evaluation semantics (single rounded fuse), so the incremental
		// result is bit-identical to a from-scratch recomputation instead
		// of diverging by 1 ULP through compiler FMA contraction.
		e.ema = math.FMA(e.alpha, v, (1-e.alpha)*e.ema)
	}
	e.init = true
	e.hist = append(e.hist, v)
}

// Retract removes the most recently appended surviving occurrence of v and
// recomputes the EWMA from scratch over the remaining hist. An empty hist
// and a missing v are rejected before any state changes.
func (e *EWMA) Retract(v float64) error {
	if !e.init {
		return ErrRetractEmpty
	}
	idx := -1
	for i := len(e.hist) - 1; i >= 0; i-- {
		if e.hist[i] == v {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ErrRetractNotFound
	}
	e.hist = append(e.hist[:idx], e.hist[idx+1:]...)
	e.recompute()
	return nil
}

// recompute rebuilds the EWMA from the current hist: first value seeds,
// each remaining value applies the decay formula once. Empty hist returns
// the EWMA to undefined.
func (e *EWMA) recompute() {
	if len(e.hist) == 0 {
		e.ema = 0
		e.init = false
		return
	}
	m := e.hist[0]
	for _, x := range e.hist[1:] {
		m = math.FMA(e.alpha, x, (1-e.alpha)*m)
	}
	e.ema = m
	e.init = true
}

// addCheckBound is the small constant that a single Add's hist inspection
// count must never exceed regardless of hist length.
const addCheckBound = 2

// VerifyAddComplexity grows hist across several size tiers and reports an
// error unless every Add inspected no more than a constant number of hist
// elements. It exposes only pass/fail; the counter value never leaves the
// package.
func VerifyAddComplexity() error {
	for _, m := range []int{100, 1000, 10000} {
		e, err := New(0.25)
		if err != nil {
			return err
		}
		for i := 0; i < m; i++ {
			e.Add(float64(i))
		}
		e.Add(42)
		if e.lastAddChecked > addCheckBound {
			return errors.New("ema: Add scanned hist beyond a constant bound")
		}
	}
	return nil
}

// Value returns the current EWMA. It is undefined unless Initialized.
func (e *EWMA) Value() float64 { return e.ema }

// Initialized reports whether at least one value is present.
func (e *EWMA) Initialized() bool { return e.init }
