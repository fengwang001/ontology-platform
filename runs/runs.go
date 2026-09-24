// Package runs splits a sequence of Unicode code points into maximal runs
// and reads/writes canonical decimal run lengths. It depends only on the
// standard library.
package runs

import (
	"math/big"
	"unicode/utf8"
)

// Run is one maximal sequence of the same code point.
type Run struct {
	Symbol rune
	Count  *big.Int
}

func newRun(symbol rune) Run {
	return Run{Symbol: symbol, Count: new(big.Int).SetInt64(1)}
}

// Split returns the maximal runs of s. Invalid UTF-8 bytes appear as RuneError
// (the caller decides whether that is an error).
func Split(s string) []Run {
	var out []Run
	for s != "" {
		r, size := utf8.DecodeRuneInString(s)
		if len(out) == 0 || out[len(out)-1].Symbol != r {
			out = append(out, newRun(r))
		} else {
			out[len(out)-1].Count.Add(out[len(out)-1].Count, big.NewInt(1))
		}
		s = s[size:]
	}
	return out
}

// AppendCount appends the canonical decimal form of n (no sign, no leading
// zeros). n must be positive.
func AppendCount(dst []byte, n *big.Int) []byte {
	return n.Append(dst, 10)
}

var zero = new(big.Int)

// IsDigits reports whether s is non-empty ASCII digits.
func IsDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// ParseCount parses canonical positive decimal counts. It rejects the empty
// string, zero, negative values and leading zeros, so a returned value never
// silently overflows a fixed-width integer.
func ParseCount(s string) (*big.Int, bool) {
	if !IsDigits(s) || s[0] == '0' {
		return nil, false
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok || n.Cmp(zero) == 0 {
		return nil, false
	}
	return n, true
}
