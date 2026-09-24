// Package splits a rune sequence into maximal runs and reads/writes
// decimal repeat counts without overflow.
package runs

import (
	"errors"
	"math/big"
	"strconv"
)

// ErrSyntax reports a non-decimal count.
var ErrSyntax = errors.New("runs: invalid decimal count")

// Run is a maximal run of N copies of the code point R.
type Run struct {
	R rune
	N int
}

// Split cuts s into maximal runs of identical code points. It performs no
// Unicode normalization: precomposed and combining sequences stay distinct.
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

// AppendCount appends the decimal form of n (no sign, no leading zeros).
func AppendCount(dst []byte, n int) []byte {
	return strconv.AppendInt(dst, int64(n), 10)
}

// ParseCount parses decimal digits into a big.Int, so counts may exceed
// int64 without any overflow. digits must be non-empty ASCII digits.
func ParseCount(digits string) (*big.Int, error) {
	if digits == "" {
		return nil, ErrSyntax
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return nil, ErrSyntax
		}
	}
	n, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return nil, ErrSyntax
	}
	return n, nil
}
