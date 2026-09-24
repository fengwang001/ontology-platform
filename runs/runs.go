// Package runs cuts a code-point sequence into maximal runs and
// reads and writes decimal run counts without overflow.
package runs

import (
	"errors"
	"math/big"
	"unicode/utf8"
)

// Run is one maximal run: Count consecutive copies of Sym.
type Run struct {
	Sym   rune
	Count *big.Int
}

var one = big.NewInt(1)

// Split cuts s into the longest possible runs of equal code points.
// No Unicode normalization is performed.
func Split(s string) []Run {
	var out []Run
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		s = s[size:]
		if n := len(out) - 1; n >= 0 && out[n].Sym == r {
			out[n].Count.Add(out[n].Count, one)
		} else {
			out = append(out, Run{Sym: r, Count: big.NewInt(1)})
		}
	}
	return out
}

// NeedsEscape reports whether r must be written as '\' + r.
func NeedsEscape(r rune) bool {
	return r == '\\' || '0' <= r && r <= '9'
}

// AppendTo appends the canonical encoding of r to dst: the decimal
// count (omitted when 1) followed by the symbol, escaped if needed.
func (r Run) AppendTo(dst []byte) []byte {
	if r.Count.Cmp(one) != 0 {
		dst = append(dst, r.Count.String()...)
	}
	if NeedsEscape(r.Sym) {
		dst = append(dst, '\\')
	}
	return utf8.AppendRune(dst, r.Sym)
}

// ErrSyntax reports a malformed decimal count.
var ErrSyntax = errors.New("runs: malformed decimal count")

// ParseCount parses a non-empty string of ASCII digits. The value may
// exceed any fixed-width integer; parsing never overflows.
func ParseCount(digits string) (*big.Int, error) {
	if digits == "" {
		return nil, ErrSyntax
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return nil, ErrSyntax
		}
	}
	n, _ := new(big.Int).SetString(digits, 10)
	return n, nil
}
