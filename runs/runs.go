// Package runs splits a string into maximal runs of equal code points and
// provides overflow-safe decimal count I/O for the RLE wire format.
package runs

import (
	"math"
	"strconv"
)

// Run is a maximal run of N consecutive copies of the code point R.
type Run struct {
	R rune
	N uint64
}

// Split returns the maximal runs of s in order. No Unicode normalization
// is performed: runs are merged only on identical code points.
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

// AppendCount appends the canonical decimal form of n to dst:
// unsigned, no leading zeros.
func AppendCount(dst []byte, n uint64) []byte {
	return strconv.AppendUint(dst, n, 10)
}

// AddDigit returns n*10+d. ok is false if the result would overflow
// uint64, in which case the caller must reject the count instead of
// silently wrapping.
func AddDigit(n uint64, d byte) (uint64, bool) {
	if n > (math.MaxUint64-uint64(d))/10 {
		return 0, false
	}
	return n*10 + uint64(d), true
}
