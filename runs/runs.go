// Package runs splits rune sequences into maximal runs and reads and
// writes decimal repeat counts without integer overflow.
package runs

import (
	"errors"
	"math/big"
)

var one = big.NewInt(1)

// Run is a maximal run of N consecutive copies of the rune Sym.
type Run struct {
	Sym rune
	N   *big.Int
}

// Split cuts s into maximal runs of equal runes. It performs no Unicode
// normalization: precomposed and decomposed forms stay distinct. s is
// assumed to be valid UTF-8.
func Split(s string) []Run {
	var out []Run
	for _, r := range s {
		if n := len(out); n > 0 && out[n-1].Sym == r {
			out[n-1].N.Add(out[n-1].N, one)
		} else {
			out = append(out, Run{Sym: r, N: big.NewInt(1)})
		}
	}
	return out
}

// AppendCount appends the canonical decimal form of n to dst, omitting
// a count of 1. n must be positive.
func AppendCount(dst []byte, n *big.Int) []byte {
	if n.Cmp(one) == 0 {
		return dst
	}
	return n.Append(dst, 10)
}

// Errors reported by ParseCount; each maps to a distinct canonical-form
// violation in the decoder.
var (
	ErrZero        = errors.New("runs: repeat count 0")
	ErrOne         = errors.New("runs: explicit repeat count 1")
	ErrLeadingZero = errors.New("runs: leading zero in repeat count")
)

// ParseCount parses a run of decimal digits as a repeat count. The count
// may be arbitrarily large. Canonical form requires no leading zeros and
// a value of at least 2 (a count of 1 is omitted in the encoding).
func ParseCount(digits string) (*big.Int, error) {
	if len(digits) > 1 && digits[0] == '0' {
		return nil, ErrLeadingZero
	}
	switch digits {
	case "0":
		return nil, ErrZero
	case "1":
		return nil, ErrOne
	}
	n, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return nil, errors.New("runs: non-digit in repeat count")
	}
	return n, nil
}
