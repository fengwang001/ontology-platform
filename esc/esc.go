// Package esc classifies the single-character escapes and \uXXXX escapes
// permitted inside a JSON string and validates UTF-16 surrogate pairs.
package esc

import "errors"

// Sentinel errors describe a single escape unit independent of byte position.
var (
	ErrUnknownEscape = errors.New("esc: unknown escape sequence")
	ErrBadHex        = errors.New("esc: \\u requires 4 hexadecimal digits")
	ErrLoneSurrogate = errors.New("esc: unpaired UTF-16 surrogate")
)

// IsSimple reports whether b is the second byte of a valid one-letter escape
// such as \" or \n.
func IsSimple(b byte) bool {
	switch b {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		return true
	}
	return false
}

// DecodeSimple returns the rune denoted by a valid one-letter escape marker.
func DecodeSimple(b byte) rune {
	switch b {
	case '"':
		return '"'
	case '\\':
		return '\\'
	case '/':
		return '/'
	case 'b':
		return 0x08
	case 'f':
		return 0x0C
	case 'n':
		return '\n'
	case 'r':
		return '\r'
	case 't':
		return '\t'
	}
	panic("esc: not a simple escape marker")
}

// IsHex reports whether b is an ASCII hexadecimal digit.
func IsHex(b byte) bool {
	return HexValue(b) >= 0
}

// HexValue returns the numeric value of a hexadecimal digit, or -1.
func HexValue(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	}
	return -1
}

// DecodeHex4 decodes exactly four hexadecimal digits beginning at s[0].
// It returns ErrBadHex when fewer than four digits are available or any byte
// is not a hexadecimal digit.
func DecodeHex4(s []byte) (rune, error) {
	if len(s) < 4 {
		return 0, ErrBadHex
	}
	var r rune
	for i := 0; i < 4; i++ {
		v := HexValue(s[i])
		if v < 0 {
			return 0, ErrBadHex
		}
		r = r<<4 | rune(v)
	}
	return r, nil
}

// IsHighSurrogate reports whether r is a UTF-16 high (leading) surrogate.
func IsHighSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDBFF }

// IsLowSurrogate reports whether r is a UTF-16 low (trailing) surrogate.
func IsLowSurrogate(r rune) bool { return r >= 0xDC00 && r <= 0xDFFF }

// IsSurrogate reports whether r occupies either surrogate half.
func IsSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDFFF }

// Pair combines a validated high and low surrogate into one scalar rune.
func Pair(high, low rune) rune {
	return 0x10000 + (high-0xD800)<<10 | (low - 0xDC00)
}
