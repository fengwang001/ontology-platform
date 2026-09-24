// Package cwin implements cumulate-window ownership math:
// floor-based large-window start, sub-window ends, Emin, lateness checks.
// It depends on no other package.
package cwin

import "errors"

// ErrInvalidParams is returned when max/step/delay constraints are violated.
var ErrInvalidParams = errors.New("cwin: invalid window parameters")

// Validate enforces max>0, step>0, delay>=0, max%step==0.
func Validate(max, step, delay int64) error {
	if max <= 0 || step <= 0 || delay < 0 || max%step != 0 {
		return ErrInvalidParams
	}
	return nil
}

// Start returns S=k*max with floor division, so [S,S+max) contains ts
// (works for negative ts).
func Start(ts, max int64) int64 {
	q, r := ts/max, ts%max
	if r < 0 { // Go truncates toward zero; floor moves the negative remainder down.
		q--
	}
	return q * max
}

// SubCount is the number of sub-windows per large window: max/step.
func SubCount(max, step int64) int { return int(max / step) }

// MinSub returns the 1-based index j of the first sub-window whose end is
// greater than ts: j = floor((ts-S)/step)+1, for ts in [S,S+max).
// ts-S is non-negative, so plain division already floors.
func MinSub(ts, start, step int64) int { return int((ts-start)/step) + 1 }

// End returns the end of sub-window j: S + j*step.
func End(start, step int64, j int) int64 { return start + int64(j)*step }
