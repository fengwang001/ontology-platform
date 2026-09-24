// Package runs splits a string into maximal runs of equal code
// points and provides overflow-safe decimal count I/O for the
// run-length codec in package rle. It depends on nothing else.
package runs

import "strconv"

// Run is a maximal sequence of N equal code points R.
type Run struct {
	R rune
	N int
}

// Split slices s into the longest possible runs of equal code
// points. No Unicode normalization is performed: s is consumed
// code point by code point and must be valid UTF-8.
func Split(s string) []Run {
	var out []Run
	for _, r := range s {
		if n := len(out); n > 0 && out[n-1].R == r {
			out[n-1].N++
		} else {
			out = append(out, Run{R: r, N: 1})
		}
	}
	return out
}

// AppendCount appends the decimal form of n to b: no sign, no
// leading zeros.
func AppendCount(b []byte, n int) []byte {
	return strconv.AppendInt(b, int64(n), 10)
}

// Counter accumulates decimal digits without overflow: the value
// saturates at the cap given to Reset, so arbitrarily long digit
// sequences are safe to read.
type Counter struct {
	val uint64
	cap uint64
}

// Reset zeroes the counter and sets a new saturation cap.
func (c *Counter) Reset(cap uint64) {
	c.val, c.cap = 0, cap
}

// Add folds one decimal digit d (0-9) into the counter.
func (c *Counter) Add(d uint64) {
	const maxUint64 = 1<<64 - 1
	if c.val > (maxUint64-9)/10 || c.val*10+d > c.cap {
		c.val = c.cap
		return
	}
	c.val = c.val*10 + d
}

// Value returns the accumulated (possibly saturated) value.
func (c *Counter) Value() uint64 {
	return c.val
}
