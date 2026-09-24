// Package runs splits strings into maximal runs and reads/writes
// decimal run counts without integer overflow.
package runs

import (
	"math"
	"strconv"
)

// Run is N consecutive copies of the code point Sym.
type Run struct {
	Sym rune
	N   uint64
}

// Split cuts s into maximal runs of equal code points. No Unicode
// normalization is performed: s is consumed code point by code point.
func Split(s string) []Run {
	var out []Run
	for _, r := range s {
		if n := len(out); n > 0 && out[n-1].Sym == r {
			out[n-1].N++
		} else {
			out = append(out, Run{Sym: r, N: 1})
		}
	}
	return out
}

// AppendCount appends the canonical decimal form of n to dst:
// unsigned, no leading zeros.
func AppendCount(dst []byte, n uint64) []byte {
	return strconv.AppendUint(dst, n, 10)
}

// Count accumulates decimal digits without overflowing. Digits beyond
// what uint64 can hold saturate the value and are reported by Value.
type Count struct {
	n   uint64
	sat bool
}

// Add folds one decimal digit d (0-9) into c.
func (c *Count) Add(d byte) {
	if c.sat {
		return
	}
	if c.n > (math.MaxUint64-uint64(d))/10 {
		c.sat = true
		c.n = math.MaxUint64
		return
	}
	c.n = c.n*10 + uint64(d)
}

// Value returns the accumulated count; ok is false if the digits
// overflowed uint64 (the returned value is then math.MaxUint64).
func (c *Count) Value() (n uint64, ok bool) { return c.n, !c.sat }
