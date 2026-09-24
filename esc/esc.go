package esc

import "errors"

// ErrSyntax marks a malformed single escape unit.
var ErrSyntax = errors.New("esc: invalid escape sequence")

// DecodeUnit decodes one complete escape unit beginning at the backslash.
// p must include the backslash. It returns the decoded value, the unit length
// in bytes, and needLow when the value is a high surrogate that must be
// immediately followed by a low-surrogate unit.
func DecodeUnit(p []byte) (r rune, size int, needLow bool, err error) {
	if len(p) < 2 || p[0] != '\\' {
		return 0, 0, false, ErrSyntax
	}
	switch p[1] {
	case '"', '\\', '/':
		return rune(p[1]), 2, false, nil
	case 'b':
		return '\b', 2, false, nil
	case 'f':
		return '\f', 2, false, nil
	case 'n':
		return '\n', 2, false, nil
	case 'r':
		return '\r', 2, false, nil
	case 't':
		return '\t', 2, false, nil
	case 'u':
		if len(p) < 6 {
			return 0, 0, false, ErrSyntax
		}
		v := 0
		for _, c := range p[2:6] {
			h, ok := HexValue(c)
			if !ok {
				return 0, 0, false, ErrSyntax
			}
			v = v<<4 | h
		}
		return rune(v), 6, rune(v) >= 0xD800 && rune(v) <= 0xDBFF, nil
	}
	return 0, 0, false, ErrSyntax
}

// IsHighSurrogate reports whether r is a UTF-16 high surrogate.
func IsHighSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDBFF }

// IsLowSurrogate reports whether r is a UTF-16 low surrogate.
func IsLowSurrogate(r rune) bool { return r >= 0xDC00 && r <= 0xDFFF }

// SurrogatePair combines a high and a low surrogate into one code point.
func SurrogatePair(high, low rune) rune {
	return 0x10000 + (high-0xD800)<<10 + (low - 0xDC00)
}

// HexValue converts one ASCII hexadecimal digit to its value.
func HexValue(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
	return int(c-'A') + 10, true
	}
	return 0, false
}
