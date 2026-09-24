// Package runs splits rune sequences into maximal runs and
// reads and writes decimal run counts without overflow.
package runs

import (
	"math/big"
	"strconv"
)

// Split calls f once per maximal run of equal runes in s, in order.
// Runs are as long as possible: no two adjacent calls share a symbol.
func Split(s string, f func(sym rune, n int)) {
	var prev rune
	n := 0
	for _, r := range s {
		if n > 0 && r == prev {
			n++
			continue
		}
		if n > 0 {
			f(prev, n)
		}
		prev, n = r, 1
	}
	if n > 0 {
		f(prev, n)
	}
}

// AppendCount appends the decimal form of n (no sign, no leading
// zeros) to dst and returns the extended slice.
func AppendCount(dst []byte, n int) []byte {
	return strconv.AppendInt(dst, int64(n), 10)
}

// ParseCount parses a non-empty string of ASCII decimal digits into
// an arbitrary-precision count. It cannot overflow; the caller must
// have validated that digits contains only '0'..'9'.
func ParseCount(digits string) *big.Int {
	n, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		panic("runs: invalid digits " + digits)
	}
	return n
}
