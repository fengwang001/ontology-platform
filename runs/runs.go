// Package runs splits a rune sequence into maximal runs and reads or writes
// decimal run counts. It has no dependencies on other packages in this module.
package runs

import (
	"iter"
	"math/big"
	"strconv"
	"strings"
)

// Split yields (symbol, count) for each maximal run of identical runes in s.
// No Unicode normalization is performed: e-acute and e plus U+0301 are
// different rune sequences.
func Split(s string) iter.Seq2[rune, int] {
	return func(yield func(rune, int) bool) {
		var sym rune
		n := 0
		for _, r := range s {
			if n > 0 && r != sym {
				if !yield(sym, n) {
					return
				}
				n = 0
			}
			sym, n = r, n+1
		}
		if n > 0 {
			yield(sym, n)
		}
	}
}

// WriteCount appends the canonical decimal form of n (n >= 2, no sign, no
// leading zeros) to b.
func WriteCount(b *strings.Builder, n int) {
	b.WriteString(strconv.Itoa(n))
}

// ReadCount parses the unsigned decimal integer beginning at s[i]. s[i] must
// be an ASCII digit. It returns the exact value (no overflow; the count may
// exceed int64) and the index just past the last consumed digit.
func ReadCount(s string, i int) (*big.Int, int) {
	j := i
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	v, _ := new(big.Int).SetString(s[i:j], 10)
	return v, j
}
