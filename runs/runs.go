// Package runs splits a rune sequence into maximal runs and reads or
// writes decimal run counts without overflow.
package runs

import (
	"math/big"
	"strconv"
)

// Run is a maximal run of N copies of the rune Sym.
type Run struct {
	Sym rune
	N   uint64
}

// Split cuts s into maximal runs of equal runes. No Unicode
// normalization is performed: é and e+U+0301 stay distinct.
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

// AppendCount appends the decimal form of n: no sign, no leading zeros.
func AppendCount(dst []byte, n uint64) []byte {
	return strconv.AppendUint(dst, n, 10)
}

// ParseCount parses decimal digits into a big integer. It never
// overflows, so counts may exceed uint64. digits must be non-empty
// and contain only ASCII digits.
func ParseCount(digits string) *big.Int {
	n, _ := new(big.Int).SetString(digits, 10)
	return n
}
