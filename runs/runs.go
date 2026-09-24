// Package runs splits rune sequences into maximal runs and reads and
// writes decimal repetition counts without overflow.
package runs

import (
	"errors"
	"math/big"
	"strconv"
	"strings"
)

// Run is a maximal run of N consecutive copies of the rune Sym.
type Run struct {
	Sym rune
	N   int
}

// Split cuts s into maximal runs of equal runes. Runes are compared as
// encoded; no Unicode normalization is performed.
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

// WriteCount appends the decimal form of n to b, omitting n == 1.
func WriteCount(b *strings.Builder, n int) {
	if n >= 2 {
		b.WriteString(strconv.Itoa(n))
	}
}

// ParseCount parses a non-empty string of ASCII digits into a big.Int,
// so counts of any magnitude are handled without overflow. Canonicality
// (no leading zeros, no 0 or 1) is the caller's concern.
func ParseCount(s string) (*big.Int, error) {
	if s == "" {
		return nil, errors.New("runs: empty count")
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return nil, errors.New("runs: non-digit in count")
		}
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, errors.New("runs: invalid count")
	}
	return n, nil
}
