// Package esc parses single JSON escape sequences (including \uXXXX and
// UTF-16 surrogate pairs) and classifies UTF-8 lead bytes.
package esc

import (
	"errors"
	"strings"
)

var (
	ErrEscape    = errors.New("unknown escape sequence")
	ErrHex       = errors.New(`\u must be followed by 4 hex digits`)
	ErrSurrogate = errors.New("unpaired UTF-16 surrogate")
)

// Seq parses one escape sequence byte by byte; the zero value is ready,
// positioned just after the opening backslash. Len counts bytes consumed.
type Seq struct {
	Len              int
	st, n, v, hi, at int
}

// Feed consumes b. done reports a completed sequence producing rune r.
// On error, off is the sequence-relative offset of the unit to blame.
func (s *Seq) Feed(b byte) (r rune, done bool, off int, err error) {
	off, s.Len = s.Len, s.Len+1
	switch s.st {
	case 0: // byte right after the backslash
		if i := strings.IndexByte(`"\/bfnrt`, b); i >= 0 {
			return rune("\"\\/\b\f\n\r\t"[i]), true, 0, nil
		}
		if b != 'u' {
			return 0, false, off, ErrEscape
		}
		s.st = 1
	case 1: // four hex digits
		h, ok := hexVal(b)
		if !ok {
			return 0, false, off, ErrHex
		}
		s.v, s.n = s.v<<4|h, s.n+1
		if s.n < 4 {
			return 0, false, 0, nil
		}
		switch at := off - 5; { // at: this unit's backslash, relative
		case s.hi != 0:
			if !isLow(s.v) {
				return 0, false, s.at, ErrSurrogate
			}
			return combine(s.hi, s.v), true, 0, nil
		case isHigh(s.v):
			s.hi, s.at, s.st, s.n, s.v = s.v, at, 2, 0, 0
		case isLow(s.v):
			return 0, false, at, ErrSurrogate
		default:
			return rune(s.v), true, 0, nil
		}
	case 2: // high surrogate read: expect '\'
		if b != '\\' {
			return 0, false, s.at, ErrSurrogate
		}
		s.st = 3
	case 3: // expect 'u'
		if b != 'u' {
			return 0, false, s.at, ErrSurrogate
		}
		s.st = 1
	}
	return 0, false, 0, nil
}

func hexVal(b byte) (int, bool) {
	i := strings.IndexByte("0123456789abcdefABCDEF", b)
	if i < 0 {
		return 0, false
	}
	if i > 15 {
		i -= 6
	}
	return i, true
}

func isHigh(v int) bool { return v >= 0xD800 && v <= 0xDBFF }
func isLow(v int) bool  { return v >= 0xDC00 && v <= 0xDFFF }

func combine(hi, lo int) rune {
	return 0x10000 + rune(hi-0xD800)<<10 + rune(lo-0xDC00)
}

// Short returns the minimal escape text for control byte b (< 0x20):
// a short form for \b \f \n \r \t, otherwise \u00xx in lowercase hex.
func Short(b byte) []byte {
	if i := strings.IndexByte("\b\f\n\r\t", b); i >= 0 {
		return []byte{'\\', "bfnrt"[i]}
	}
	const hexd = "0123456789abcdef"
	return []byte{'\\', 'u', '0', '0', hexd[b>>4], hexd[b&0xF]}
}

// UTF8 tracks an in-progress multi-byte UTF-8 sequence: Need counts the
// continuation bytes still expected; the next must lie in [Lo, Hi].
type UTF8 struct {
	Need   int
	Lo, Hi byte
}

// Feed consumes one continuation byte, reporting whether it is valid.
func (u *UTF8) Feed(b byte) bool {
	if b < u.Lo || b > u.Hi {
		return false
	}
	u.Need--
	u.Lo, u.Hi = 0x80, 0xBF
	return true
}

// Lead classifies a non-ASCII lead byte, rejecting stray continuations,
// overlong forms, UTF-8 encoded surrogates, and values above U+10FFFF.
func Lead(b byte) (UTF8, bool) {
	switch {
	case b >= 0xC2 && b <= 0xDF:
		return UTF8{1, 0x80, 0xBF}, true
	case b == 0xE0:
		return UTF8{2, 0xA0, 0xBF}, true
	case b >= 0xE1 && b <= 0xEC || b == 0xEE || b == 0xEF:
		return UTF8{2, 0x80, 0xBF}, true
	case b == 0xED:
		return UTF8{2, 0x80, 0x9F}, true
	case b == 0xF0:
		return UTF8{3, 0x90, 0xBF}, true
	case b >= 0xF1 && b <= 0xF3:
		return UTF8{3, 0x80, 0xBF}, true
	case b == 0xF4:
		return UTF8{3, 0x80, 0x8F}, true
	}
	return UTF8{}, false
}
