package hll

import (
	"errors"
	"fmt"
)

// Merge returns a new Estimator whose registers are the element-wise maximum
// of the registers of a and b. Neither input is modified: Merge is a pure
// function of the two sketches.
//
// The returned mode (sparse or dense) is the "earlier" of the two:
//   - Both sparse: the sparse hash sets are unioned, so the merged result
//     keeps exact counts up to the same limit.
//   - Either side dense: only registers survive (the dense side no longer
//     remembers individual hashes), and the result is dense.
//
// Merging an estimator with an empty one is the identity: the result's
// register snapshot is byte-for-byte equal to the non-empty side's.
//
// Merge fails with a decidable error when the precisions differ; the error
// text reports both p values so callers can distinguish the case by
// errors.Is / inspection rather than string guessing.
var ErrPrecisionMismatch = errors.New("hll: cannot merge estimators with different precision p")

// PrecisionMismatchError carries the two p values involved in a failed Merge.
type PrecisionMismatchError struct {
	P1 int
	P2 int
}

func (e PrecisionMismatchError) Error() string {
	return fmt.Sprintf("%s: p1=%d p2=%d", ErrPrecisionMismatch, e.P1, e.P2)
}

// Is reports a match against ErrPrecisionMismatch so errors.Is works.
func (e PrecisionMismatchError) Is(target error) bool { return target == ErrPrecisionMismatch }

// Merge combines two estimators; see the type docs for semantics.
func Merge(a, b *Estimator) (*Estimator, error) {
	if a == nil || b == nil {
		return nil, errors.New("hll: Merge requires non-nil estimators")
	}
	if a.p != b.p {
		return nil, PrecisionMismatchError{P1: a.p, P2: b.p}
	}

	out, _ := New(a.p)
	for i := range out.regs {
		switch {
		case a.regs[i] >= b.regs[i]:
			out.regs[i] = a.regs[i]
		default:
			out.regs[i] = b.regs[i]
		}
	}

	if a.dense || b.dense {
		out.dense = true
		out.seen = nil
		out.distinct = 0
		return out, nil
	}

	for h := range a.seen {
		out.seen[h] = struct{}{}
	}
	for h := range b.seen {
		out.seen[h] = struct{}{}
	}
	out.distinct = len(out.seen)
	if out.distinct >= sparseLimit {
		out.seen = nil
		out.dense = true
	}
	return out, nil
}
