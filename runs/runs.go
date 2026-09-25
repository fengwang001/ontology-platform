// Package runs provides maximal-run splitting and overflow-free decimal
// count read/write for the escaped RLE codec in package rle.
package runs

import (
	"errors"
	"math/big"
	"strconv"
	"unicode/utf8"
)

// Decimal count rules enforced while reading a run count.
var (
	ErrLeadingZero = errors.New("runs: count with leading zero")
	ErrCountZero   = errors.New("runs: zero count")
	ErrCountOne    = errors.New("runs: explicit count of 1")
)

// Each calls f once for every maximal run of identical runes in s.
func Each(s string, f func(sym rune, n int)) {
	for i := 0; i < len(s); {
		sym, w := utf8.DecodeRuneInString(s[i:])
		j := i + w
		for j < len(s) {
			next, nw := utf8.DecodeRuneInString(s[j:])
			if next != sym {
				break
			}
			j += nw
		}
		f(sym, (j-i)/w)
		i = j
	}
}

// AppendCount appends the unsigned decimal form of n to dst.
func AppendCount(dst []byte, n int) []byte {
	return strconv.AppendInt(dst, int64(n), 10)
}

// Count is an overflow-free non-negative decimal accumulator.
type Count struct {
	v       big.Int
	started bool
}

var ten = big.NewInt(10)

// Reset makes c equal to zero.
func (c *Count) Reset() { c.v.SetInt64(0); c.started = false }

// Add appends one decimal digit d (0..9), rejecting a second digit after a
// leading zero.
func (c *Count) Add(d byte) error {
	if c.started && c.v.Sign() == 0 {
		return ErrLeadingZero
	}
	c.started = true
	c.v.Mul(&c.v, ten)
	c.v.Add(&c.v, big.NewInt(int64(d)))
	return nil
}

// Done finalizes a count, rejecting 0 and 1 (1 is never written explicitly).
func (c *Count) Done() error {
	if !c.started {
		return nil
	}
	if c.v.Sign() == 0 {
		return ErrCountZero
	}
	if c.v.IsInt64() && c.v.Int64() == 1 {
		return ErrCountOne
	}
	return nil
}

// Fits reports whether c fits within limit (limit >= 0).
func (c *Count) Fits(limit int64) bool { return c.v.IsInt64() && c.v.Int64() <= limit }

// Int64 returns c as an int64; only valid when Fits(int64 max).
func (c *Count) Int64() int64 { return c.v.Int64() }

// AppendRun appends one canonical run: the decimal count when n >= 2, the
// escaping backslash for digits and '\\', then the symbol itself.
func AppendRun(dst []byte, sym rune, n int) []byte {
	if n >= 2 {
		dst = AppendCount(dst, n)
	}
	if sym < utf8.RuneSelf && (sym == '\\' || IsDigit(byte(sym))) {
		dst = append(dst, '\\')
	}
	return utf8.AppendRune(dst, sym)
}

// IsDigit reports whether b is an ASCII digit.
func IsDigit(b byte) bool { return '0' <= b && b <= '9' }

// Lead validates a UTF-8 lead byte and returns the number of bytes in the
// rune (2..4) and the inclusive range allowed for the next byte.
func Lead(b byte) (width int, lo, hi byte, ok bool) {
	switch {
	case 0xC2 <= b && b <= 0xDF:
		return 2, 0x80, 0xBF, true
	case b == 0xE0:
		return 3, 0xA0, 0xBF, true
	case b == 0xED:
		return 3, 0x80, 0x9F, true
	case 0xE1 <= b && b <= 0xEF:
		return 3, 0x80, 0xBF, true
	case b == 0xF0:
		return 4, 0x90, 0xBF, true
	case b == 0xF4:
		return 4, 0x80, 0x8F, true
	case 0xF1 <= b && b <= 0xF3:
		return 4, 0x80, 0xBF, true
	}
	return 0, 0, 0, false
}
