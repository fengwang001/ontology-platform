// Package seq holds the single-sequence-number predicates used by the
// gap detector. It depends on no other package.
package seq

import "math"

// Valid reports whether s is a legal sequence number: stream numbers
// start at 1, so anything <= 0 is rejected.
func Valid(s int64) bool { return s > 0 }

// Covered reports whether s is already at or below the watermark h,
// i.e. inside the confirmed contiguous prefix (seen or judged a gap).
// Such arrivals must be ignored without touching any state.
func Covered(s, h int64) bool { return s <= h }

// Overdue reports whether the still-missing number x has missed the
// reorder window: an observed number maxSeen at least w ahead of x
// (maxSeen-x >= w) proves x is lost. The subtraction form is used on
// purpose so the comparison never computes x+w and cannot overflow.
func Overdue(x, maxSeen, w int64) bool { return maxSeen-x >= w }

// InRange reports that accepting s leaves the convergence arithmetic
// safe: with s <= MaxInt64-w, no expression x+w evaluated for a water
// mark x <= s can overflow int64.
func InRange(s, w int64) bool { return s <= math.MaxInt64-w }
