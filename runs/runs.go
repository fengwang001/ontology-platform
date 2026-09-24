// Package runs splits a sequence of Unicode code points into maximal runs
// and provides overflow-safe decimal handling of run lengths.
package runs

import (
	"math/big"
	"strconv"
	"unicode/utf8"
)

// Run is a maximal sequence of equal code points.
type Run struct {
	Rune  rune
	Count uint64
}

// Split calls yield once for every maximal run in s. Equal code points are
// compared as runes; no Unicode normalization is performed. Iteration stops
// if yield returns false.
func Split(s string, yield func(Run) bool) {
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		n := uint64(1)
		for rest := s[size:]; len(rest) > 0; rest = rest[size:] {
			r2, next := utf8.DecodeRuneInString(rest)
			if r2 != r {
				break
			}
			size += next
			n++
		}
		if !yield(Run{Rune: r, Count: n}) {
			return
		}
		s = s[size:]
	}
}

// Accum appends decimal digit d to n as in n = n*10 + d, mutating and
// returning n. Arbitrarily large values never overflow.
func Accum(n *big.Int, d byte) *big.Int {
	if n == nil {
		n = new(big.Int)
	}
	return n.Mul(n, big.NewInt(10)).Add(n, big.NewInt(int64(d-'0')))
}

// AppendCount appends the canonical decimal form of n (unsigned, no leading
// zero) to dst and returns the extended buffer.
func AppendCount(dst []byte, n uint64) []byte {
	return strconv.AppendUint(dst, n, 10)
}
