// Package mset tracks the two-side multiplicities of a single value and
// decides when an INTERSECT ALL crossing changes the intersection multiplicity.
package mset

import "errors"

// Side selects one of the two input relations.
type Side int

const (
	L Side = iota
	R
)

// NullMarker is the reserved string denoting NULL. NULL never matches
// anything, itself included.
const NullMarker = "<null>"

// IsNull reports whether val is the reserved NULL marker.
func IsNull(val string) bool { return val == NullMarker }

var (
	// ErrZeroDelta is returned when d == 0.
	ErrZeroDelta = errors.New("mset: delta must be non-zero")
	// ErrCountNegative is returned when an decrement would make a count negative.
	ErrCountNegative = errors.New("mset: count must not become negative")
	// ErrBadSide is returned when side is neither L nor R.
	ErrBadSide = errors.New("mset: side must be L or R")
)

// Cell holds the L and R copy counts of one value.
type Cell struct {
	l int
	r int
}

// Counts returns the current left and right counts.
func (c *Cell) Counts() (l, r int) { return c.l, c.r }

// Mult returns the current intersection multiplicity m = min(l, r).
func (c *Cell) Mult() int { return min(c.l, c.r) }

// Apply adds d to the count of side and returns the signed change of the
// intersection multiplicity (new m minus old m). The change is non-zero only
// when the smaller side actually crosses the other side; surplus changes on
// the larger side return 0. A rejected Apply leaves the Cell untouched.
func (c *Cell) Apply(side Side, d int) (int, error) {
	if d == 0 {
		return 0, ErrZeroDelta
	}
	switch side {
	case L:
		if c.l+d < 0 {
			return 0, ErrCountNegative
		}
		old := c.Mult()
		c.l += d
		return c.Mult() - old, nil
	case R:
		if c.r+d < 0 {
			return 0, ErrCountNegative
		}
		old := c.Mult()
		c.r += d
		return c.Mult() - old, nil
	default:
		return 0, ErrBadSide
	}
}
