// Package runs splits code-point sequences into maximal runs and
// converts run counts to and from decimal without overflow.
package runs

import (
	"math/big"
	"strconv"
)

// Run is a maximal run of identical code points.
type Run struct {
	Rune  rune
	Count uint64
}

// Split slices s into the longest possible runs of equal code points.
// It compares code points as-is and performs no Unicode normalization.
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

// NeedsEscape reports whether r must be written as '\' + byte(r):
// exactly the ASCII digits and the backslash.
func NeedsEscape(r rune) bool {
	return r == '\\' || ('0' <= r && r <= '9')
}

// AppendCount appends the decimal form of n: unsigned, no leading zeros.
func AppendCount(dst []byte, n uint64) []byte {
	return strconv.AppendUint(dst, n, 10)
}

// ParseCount parses a decimal count of arbitrary size, so counts may
// exceed int64 without overflow. ok is false for empty input or input
// containing a non-digit.
func ParseCount(digits string) (n *big.Int, ok bool) {
	if digits == "" {
		return nil, false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return nil, false
		}
	}
	n, _ = new(big.Int).SetString(digits, 10)
	return n, true
}
