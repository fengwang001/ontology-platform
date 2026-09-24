// Package seq holds the per-sequence-number predicates of the gap detector.
// It depends on no other package of this module.
package seq

import (
	"errors"
	"math"
)

// ErrInvalidSeq is returned for sequence numbers that are not positive.
var ErrInvalidSeq = errors.New("seq: invalid sequence number: must be > 0")

// ErrSeqOverflow is returned when a sequence number is so large that adding
// the reorder window to it could overflow int64.
var ErrSeqOverflow = errors.New("seq: sequence number overflows window arithmetic")

// Valid reports whether s is a legal sequence number (seq > 0).
func Valid(s int64) bool { return s > 0 }

// Covered reports whether s is already at or below the contiguous-prefix
// watermark h: the number was either seen or already declared a gap.
func Covered(s, h int64) bool { return s <= h }

// MissedWindow reports whether the first missing position h1 (h1 = H+1) has
// missed the reorder window of width w: some seen number is at least h1+w
// away, i.e. maxSeen-h1 >= w. h1 is always >= 1 here, so the subtraction
// cannot overflow.
func MissedWindow(h1, maxSeen, w int64) bool { return maxSeen-h1 >= w }

// Overflow reports whether admitting seq would make the later comparison
// h1+w (h1 <= seq) overflow int64. Caller guarantees w > 0.
func Overflow(seq, w int64) bool { return seq > math.MaxInt64-w }
