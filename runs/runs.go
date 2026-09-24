// Package runs splits a rune stream into maximal runs and performs
// overflow-safe decimal reading/writing of run lengths. It has no dependencies.
package runs

import (
	"strconv"
	"unicode/utf8"
)

// Run is one maximal run: Sym repeated Count times.
type Run struct {
	Sym   rune
	Count uint64
}

// Longest splits s into maximal runs of equal code points.
// No Unicode normalization is performed.
func Longest(s string) []Run {
	var out []Run
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		count := uint64(1)
		for rest := s[size:]; len(rest) > 0; {
			next, nextSize := utf8.DecodeRuneInString(rest)
			if next != r {
				break
			}
			count++
			size += nextSize
			rest = s[size:]
		}
		out = append(out, Run{Sym: r, Count: count})
	s = s[size:]
	}
	return out
}

// AppendCount appends the canonical decimal form of n (unsigned, no leading
// zeros); n must be >= 1. Count 1 is the caller's responsibility to omit.
func AppendCount(dst []byte, n uint64) []byte {
	return strconv.AppendUint(dst, n, 10)
}

// AddDigit appends one decimal digit to n without overflowing. The boolean
// reports whether the value stayed within uint64 range.
func AddDigit(n uint64, d byte) (uint64, bool) {
	const cutoff = ^uint64(0) / 10
	digit := uint64(d - '0')
	if n > cutoff || (n == cutoff && digit > ^uint64(0)%10) {
		return n, false
	}
	return n*10 + digit, true
}

// Fits reports whether n copies of a rune of encoded byte size symLen fit in
// the remaining budget, without overflowing the multiplication.
func Fits(n uint64, symLen, remaining int) bool {
	if n > uint64(remaining) {
		return false
	}
	return uint64(symLen) <= uint64(remaining)/n
}
