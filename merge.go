package hll

import (
	"errors"
	"fmt"
)

// ErrPrecisionMismatch is returned by Merge when the two estimators were
// constructed with different precision values.
var ErrPrecisionMismatch = errors.New("hll: cannot merge estimators with different precision")

// Merge returns a new estimator whose registers are the per-register maximum
// of a and b, which must have the same precision p. Neither a nor b is
// modified: the result is an independent copy.
//
// Merging an empty estimator is an identity operation. It returns
// ErrPrecisionMismatch (wrapping that value) when the precisions differ; use
// errors.Is to detect it, and inspect the error text for the two p values.
func Merge(a, b *Estimator) (*Estimator, error) {
	if a == nil || b == nil {
		return nil, errors.New("hll: cannot merge nil estimators")
	}
	if a.p != b.p {
		return nil, fmt.Errorf("%w: left p=%d, right p=%d",
			ErrPrecisionMismatch, a.p, b.p)
	}
	out := &Estimator{
		p:         a.p,
		registers: make([]uint8, len(a.registers)),
	}
	for i := range a.registers {
		x, y := a.registers[i], b.registers[i]
		if x >= y {
			out.registers[i] = x
		} else {
			out.registers[i] = y
		}
	}
	return out, nil
}
