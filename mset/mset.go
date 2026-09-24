// Package mset tracks the two-side multiplicities of a single value and
// derives its INTERSECT ALL multiplicity m = min(l, r). NULL never matches.
package mset

import "errors"

// Side identifies which input relation an operation applies to.
type Side int

const (
	L Side = iota
	R
)

// Null is the reserved string representing a NULL element; it never matches.
const Null = "<null>"

var (
	// ErrZeroDelta rejects Apply with d == 0.
	ErrZeroDelta = errors.New("mset: delta must be non-zero")
	// ErrNegativeCount rejects Apply that would drive a count below zero.
	ErrNegativeCount = errors.New("mset: count would become negative")
)

// Counter holds the per-side multiplicities of one value.
type Counter struct {
	l, r int
	null bool
}

// New returns a Counter; null must be true iff the value is the NULL marker.
func New(null bool) *Counter { return &Counter{null: null} }

// M returns the current intersection multiplicity (always 0 for NULL).
func (c *Counter) M() int {
	if c.null {
		return 0
	}
	if c.l < c.r {
		return c.l
	}
	return c.r
}

// Empty reports whether both sides are back at zero copies.
func (c *Counter) Empty() bool { return c.l == 0 && c.r == 0 }

// Apply adds d (non-zero) to one side and returns the change in M.
// It validates fully before mutating, so a rejected call leaves no trace.
func (c *Counter) Apply(s Side, d int) (int, error) {
	if d == 0 {
		return 0, ErrZeroDelta
	}
	if s == L {
		if c.l+d < 0 {
			return 0, ErrNegativeCount
		}
	} else if c.r+d < 0 {
		return 0, ErrNegativeCount
	}
	old := c.M()
	if s == L {
		c.l += d
	} else {
		c.r += d
	}
	return c.M() - old, nil
}
