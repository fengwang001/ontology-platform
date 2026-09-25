// Package runs splits a string into maximal runs of equal code points
// and reads/writes decimal repetition counts without overflow.
package runs

import (
	"bytes"
	"errors"
	"io"
	"math/big"
	"unicode/utf8"
)

var one = big.NewInt(1)

// Sentinels for non-canonical counts, returned by ParseCount.
var (
	ErrCountOne    = errors.New("runs: explicit count 1")
	ErrCountZero   = errors.New("runs: count 0")
	ErrLeadingZero = errors.New("runs: count with leading zero")
)

// ErrOutputLimit reports that expanding a run would exceed the byte limit.
var ErrOutputLimit = errors.New("runs: output byte limit exceeded")

// Run is Count consecutive copies of the code point Sym.
// Count is never nil and always >= 1.
type Run struct {
	Count *big.Int
	Sym   rune
}

// Split cuts s into the longest possible runs of equal code points.
// It performs no Unicode normalization: precomposed and combining
// forms are different code points and are never merged.
func Split(s string) []Run {
	var rs []Run
	for i := 0; i < len(s); {
		r, sz := utf8.DecodeRuneInString(s[i:])
		j := i + sz
		for j < len(s) {
			r2, sz2 := utf8.DecodeRuneInString(s[j:])
			if r2 != r || sz2 != sz {
				break
			}
			j += sz2
		}
		rs = append(rs, Run{big.NewInt(int64((j - i) / sz)), r})
		i = j
	}
	return rs
}

// IsOne reports whether n equals 1 (a count that must be omitted).
func IsOne(n *big.Int) bool { return n.Cmp(one) == 0 }

// Decimal returns the canonical decimal form of n (n >= 2):
// unsigned, no leading zeros.
func Decimal(n *big.Int) string { return n.String() }

// ParseCount parses canonical count digits: no leading zeros, not 0
// (a run of zero symbols) and not 1 (must be omitted). It never
// overflows regardless of the digit count.
func ParseCount(digits string) (*big.Int, error) {
	switch {
	case len(digits) > 1 && digits[0] == '0':
		return nil, ErrLeadingZero
	case digits[0] == '0':
		return nil, ErrCountZero
	case len(digits) == 1 && digits[0] == '1':
		return nil, ErrCountOne
	}
	n, _ := new(big.Int).SetString(digits, 10)
	return n, nil
}

// AppendTo writes the canonical encoding of r to buf: the decimal
// count (omitted when 1) followed by the symbol, escaping digits
// and '\' with a backslash.
func (r Run) AppendTo(buf *bytes.Buffer) {
	if !IsOne(r.Count) {
		buf.WriteString(Decimal(r.Count))
	}
	if r.Sym == '\\' || '0' <= r.Sym && r.Sym <= '9' {
		buf.WriteByte('\\')
	}
	buf.WriteRune(r.Sym)
}

// Expand writes n copies of sym to w in bounded chunks; it never
// allocates proportionally to n. If limit > 0, at most limit-done
// bytes are written and ErrOutputLimit is returned once the run
// exceeds the limit. n is consumed. It returns bytes written.
func Expand(w io.Writer, sym rune, n *big.Int, limit, done int64) (int64, error) {
	var ub [utf8.UTFMax]byte
	sz := int64(utf8.EncodeRune(ub[:], sym))
	chunk := bytes.Repeat(ub[:sz], 8192/int(sz))
	per := big.NewInt(int64(len(chunk)) / sz)
	max := int64(0)
	if limit > 0 {
		max = limit - done
	}
	var out int64
	for n.Sign() > 0 {
		k := int64(len(chunk))
		if n.Cmp(per) < 0 {
			k = n.Int64() * sz
		}
		if max > 0 && out+k > max {
			if k = (max - out) / sz * sz; k == 0 {
				return out, ErrOutputLimit
			}
		}
		if _, err := w.Write(chunk[:k]); err != nil {
			return out, err
		}
		out += k
		n.Sub(n, big.NewInt(k/sz))
	}
	return out, nil
}
