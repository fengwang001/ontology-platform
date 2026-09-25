// Package ema implements an exponentially weighted moving average
// with exact retraction. It keeps the full history of live values
// so a retracted value can be removed and the EWMA recomputed
// exactly, as if the value had never been added.
package ema

import "errors"

// Sentinel errors, distinguishable via errors.Is.
var (
	ErrInvalidAlpha = errors.New("ema: alpha out of range (0,1]")
	ErrNotFound     = errors.New("ema: value not present in history")
	ErrEmpty        = errors.New("ema: retract from empty sequence")
)

// EMA holds the decay state plus the live history. Not safe for
// concurrent use; callers serialize access.
type EMA struct {
	alpha float64
	hist  []float64 // live values, in insertion order
	value float64   // current EWMA, meaningful only when init is true
	init  bool

	// lastAddChecked records how many hist elements the most recent
	// Add inspected. Add is O(1) decay, so this stays 0. Unexported
	// on purpose: it must never leak through the public API.
	lastAddChecked int
}

// New validates alpha and returns an empty (uninitialized) EMA.
func New(alpha float64) (*EMA, error) {
	if !(alpha > 0 && alpha <= 1) {
		return nil, ErrInvalidAlpha
	}
	return &EMA{alpha: alpha}, nil
}

// Add folds v into the EWMA. The first value seeds the EWMA
// directly (no zero-seed bias); later values use the decay formula.
// Add never rescans hist: it only appends.
func (e *EMA) Add(v float64) {
	e.lastAddChecked = 0
	if !e.init {
		e.value = v
		e.init = true
	} else {
		e.value = e.alpha*v + (1-e.alpha)*e.value
	}
	e.hist = append(e.hist, v)
}

// Retract removes the most recently added occurrence of v and
// recomputes the EWMA from scratch over the remaining history.
// On any failure the receiver is left completely untouched.
func (e *EMA) Retract(v float64) error {
	if len(e.hist) == 0 {
		return ErrEmpty
	}
	idx := -1
	for i := len(e.hist) - 1; i >= 0; i-- {
		if e.hist[i] == v {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ErrNotFound
	}
	e.hist = append(e.hist[:idx], e.hist[idx+1:]...)
	e.recompute()
	return nil
}

// recompute rebuilds the EWMA from hist: first value is the seed,
// each subsequent value applies ema = alpha*x + (1-alpha)*ema.
func (e *EMA) recompute() {
	if len(e.hist) == 0 {
		e.value = 0
		e.init = false
		return
	}
	v := e.hist[0]
	for _, x := range e.hist[1:] {
		v = e.alpha*x + (1-e.alpha)*v
	}
	e.value = v
	e.init = true
}

// Value returns the current EWMA. Meaningless when uninitialized.
func (e *EMA) Value() float64 { return e.value }

// Initialized reports whether at least one live value exists.
func (e *EMA) Initialized() bool { return e.init }

// Len returns the number of live (added, not retracted) values.
func (e *EMA) Len() int { return len(e.hist) }
