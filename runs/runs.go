// Package runs splits code point sequences into maximal runs and
// reads and writes decimal run counts. It depends on nothing but
// the standard library.
package runs

import (
	"errors"
	"math/big"
	"strconv"
)

// Sentinel errors, distinguishable with errors.Is.
var (
	ErrExplicitOne = errors.New("runs: count of 1 must be omitted")
	ErrZeroCount   = errors.New("runs: count of 0 is not allowed")
	ErrLeadingZero = errors.New("runs: count has a leading zero")
	ErrInvalidUTF8 = errors.New("runs: invalid UTF-8")
)

// Run is a maximal run of N copies of the code point R.
type Run struct {
	R rune
	N int
}

// Split returns the longest-run decomposition of s, which must be
// valid UTF-8. No Unicode normalization is performed.
func Split(s string) []Run {
	var out []Run
	for _, r := range s {
		if n := len(out); n > 0 && out[n-1].R == r {
			out[n-1].N++
		} else {
			out = append(out, Run{r, 1})
		}
	}
	return out
}

// AppendCount appends the canonical decimal form of n (n >= 2) to dst.
func AppendCount(dst []byte, n int) []byte {
	return strconv.AppendInt(dst, int64(n), 10)
}

// Count parses a canonical decimal count: the empty string means an
// omitted count (1); otherwise no leading zeros and neither 0 nor 1.
// The value may be arbitrarily large and never overflows.
func Count(digits string) (*big.Int, error) {
	if digits == "" {
		return big.NewInt(1), nil
	}
	if digits[0] == '0' {
		if len(digits) == 1 {
			return nil, ErrZeroCount
		}
		return nil, ErrLeadingZero
	}
	if digits == "1" {
		return nil, ErrExplicitOne
	}
	n, _ := new(big.Int).SetString(digits, 10)
	return n, nil
}

// RuneScanner decodes UTF-8 code points one byte at a time, so a
// stream can be validated without ever re-examining a byte.
type RuneScanner struct {
	need int  // continuation bytes still expected
	val  rune // value accumulated so far
	min  rune // smallest legal value (rejects overlong forms)
}

// Pending reports whether a partial code point is buffered.
func (s *RuneScanner) Pending() bool { return s.need > 0 }

// Feed consumes one byte. It returns (r, true, nil) when a code point
// completes, (0, false, nil) when more bytes are needed, and
// ErrInvalidUTF8 on any malformed byte.
func (s *RuneScanner) Feed(b byte) (rune, bool, error) {
	if s.need == 0 {
		switch {
		case b < 0x80:
			return rune(b), true, nil
		case b < 0xC2:
			return 0, false, ErrInvalidUTF8
		case b < 0xE0:
			s.need, s.val, s.min = 1, rune(b)&0x1F, 0x80
		case b < 0xF0:
			s.need, s.val, s.min = 2, rune(b)&0x0F, 0x800
		case b < 0xF5:
			s.need, s.val, s.min = 3, rune(b)&0x07, 0x10000
		default:
			return 0, false, ErrInvalidUTF8
		}
		return 0, false, nil
	}
	if b < 0x80 || b >= 0xC0 {
		s.need = 0
		return 0, false, ErrInvalidUTF8
	}
	s.val = s.val<<6 | rune(b)&0x3F
	s.need--
	if s.need > 0 {
		return 0, false, nil
	}
	r := s.val
	if r < s.min || (r >= 0xD800 && r < 0xE000) {
		return 0, false, ErrInvalidUTF8
	}
	return r, true, nil
}
