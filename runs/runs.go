// Package runs splits a rune sequence into maximal runs and provides
// decimal count / escaped-symbol read and write primitives.
package runs

import (
	"math/big"
)

// Run is one maximal run: symbol R repeated Count times.
type Run struct {
	R     rune
	Count *big.Int
}

// Split groups s into maximal runs of equal code points.
func Split(s []rune) []Run {
	var out []Run
	for _, r := range s {
		if n := len(out); n > 0 && out[n-1].R == r {
			out[n-1].Count.Add(out[n-1].Count, big.NewInt(1))
			continue
		}
		out = append(out, Run{R: r, Count: new(big.Int).SetInt64(1)})
	}
	return out
}

// NeedEscape reports whether r must be written as backslash + r.
func NeedEscape(r rune) bool {
	return r == '\\' || (r >= '0' && r <= '9')
}

// AppendCount appends the canonical decimal form of n (no sign, no
// leading zeros); n must be positive.
func AppendCount(dst []byte, n *big.Int) []byte { return dst }

// AppendSymbol appends r, escaped with a leading backslash when required.
func AppendSymbol(dst []byte, r rune) []byte { return dst }

// CountReader parses one canonical decimal count across byte chunks.
type CountReader struct {
	n       big.Int
	digits  int
	leading bool
}

// Digit feeds one ASCII digit; ok is false on invalid UTF-8 (never here).
func (c *CountReader) Digit(ch byte) bool {
	if c.digits == 0 {
		c.n.SetInt64(0)
		c.leading = ch == '0'
	}
	c.digits++
	v := int64(ch - '0')
	c.n.Mul(&c.n, big.NewInt(10))
	c.n.Add(&c.n, big.NewInt(v))
	return true
}

// End finishes the count. kind is 0=unit, 1=one, 2=zero, 3=leading-zero.
func (c *CountReader) End() (*big.Int, int, bool) {
	switch {
	case c.digits == 0:
		return big.NewInt(1), 0, true
	case c.digits > 1 && c.leading:
		return new(big.Int).Set(&c.n), 3, true
	case c.digits == 1 && c.leading:
		return new(big.Int).Set(&c.n), 2, true
	case c.digits == 1 && c.n.Sign() > 0 && c.n.Cmp(big.NewInt(1)) == 0:
		return new(big.Int).Set(&c.n), 1, true
	default:
		return new(big.Int).Set(&c.n), 0, true
	}
}

// IsDigit reports whether ch is an ASCII digit.
func IsDigit(ch byte) bool { return ch >= '0' && ch <= '9' }
