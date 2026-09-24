// Package order defines the total order over (TS, Seq) pairs, lateness
// detection and watermark advancement. It depends on nothing.
package order

import "cmp"

// Less reports whether (ts1, seq1) sorts strictly before (ts2, seq2).
func Less(ts1, seq1, ts2, seq2 int64) bool {
	return Compare(ts1, seq1, ts2, seq2) < 0
}

// Compare returns -1, 0 or +1 comparing (ts1, seq1) with (ts2, seq2).
func Compare(ts1, seq1, ts2, seq2 int64) int {
	if c := cmp.Compare(ts1, ts2); c != 0 {
		return c
	}
	return cmp.Compare(seq1, seq2)
}

// Late reports whether an event with timestamp ts is late with respect to
// the watermark wm. hasWM is false before any event was accepted (the
// watermark is minus infinity), in which case nothing is late.
func Late(ts, wm int64, hasWM bool) bool {
	return hasWM && ts <= wm
}

// Advance returns the watermark after an event with timestamp ts has been
// accepted: wm = max(wm, ts-delay). It never moves backwards.
func Advance(wm int64, hasWM bool, ts, delay int64) (int64, bool) {
	if !hasWM || ts-delay > wm {
		return ts - delay, true
	}
	return wm, true
}
