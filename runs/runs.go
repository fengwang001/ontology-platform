// Package runs splits rune sequences into maximal runs and reads and
// writes decimal run counts without overflow.
package runs

import "math/big"

var one = big.NewInt(1)

// Run is a maximal run of Count consecutive copies of Symbol.
type Run struct {
	Symbol rune
	Count  *big.Int
}

// Escaped reports whether r must be backslash-escaped in encoded text.
func Escaped(r rune) bool { return r == '\\' || '0' <= r && r <= '9' }

// Split divides s into maximal runs of identical runes.
func Split(s string) []Run {
	var out []Run
	for _, r := range s {
		if n := len(out); n > 0 && out[n-1].Symbol == r {
			out[n-1].Count.Add(out[n-1].Count, one)
		} else {
			out = append(out, Run{Symbol: r, Count: new(big.Int).Set(one)})
		}
	}
	return out
}

// ParseCount converts a non-empty ASCII digit string to a big.Int.
// The digit string length is unbounded, so the result cannot overflow.
func ParseCount(digits string) *big.Int {
	n, _ := new(big.Int).SetString(digits, 10)
	return n
}
