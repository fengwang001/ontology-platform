// Package runs splits strings into maximal runs of equal code points and
// reads and writes decimal repeat counts without overflow.
package runs

import "strconv"

// Run is Count consecutive copies of Rune.
type Run struct {
	Rune  rune
	Count uint64
}

// Split cuts s into the longest possible runs of equal code points.
// No Unicode normalization is performed.
func Split(s string) []Run {
	var out []Run
	for _, r := range s {
		if n := len(out); n > 0 && out[n-1].Rune == r {
			out[n-1].Count++
		} else {
			out = append(out, Run{Rune: r, Count: 1})
		}
	}
	return out
}

// AppendDecimal appends n in decimal, with no sign and no leading zeros.
func AppendDecimal(dst []byte, n uint64) []byte {
	return strconv.AppendUint(dst, n, 10)
}

// AddDigit returns n*10+d. ok is false when the true result would exceed
// max; n is then saturated to max+1 (max must be below math.MaxUint64).
func AddDigit(n, d, max uint64) (uint64, bool) {
	if n > max/10 || n == max/10 && d > max%10 {
		return max + 1, false
	}
	return n*10 + d, true
}
