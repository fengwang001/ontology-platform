// Package esc classifies and validates single JSON string escape sequences,
// including UTF-16 surrogate pairs. It has no dependencies.
package esc

const hexdigit = "0123456789abcdef0123456789ABCDEF"

// IsHexDigit reports whether b is an ASCII hexadecimal digit.
func IsHexDigit(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}

// HexValue returns the value of an ASCII hexadecimal digit: 0..15.
// For non-hex bytes the result is undefined.
func HexValue(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	default:
		return int(b-'A') + 10
	}
}

// IsHighSurrogate reports whether cp is a UTF-16 high surrogate (D800–DBFF).
func IsHighSurrogate(cp rune) bool { return cp >= 0xD800 && cp <= 0xDBFF }

// IsLowSurrogate reports whether cp is a UTF-16 low surrogate (DC00–DFFF).
func IsLowSurrogate(cp rune) bool { return cp >= 0xDC00 && cp <= 0xDFFF }

// IsSurrogate reports whether cp is any UTF-16 surrogate (D800–DFFF).
func IsSurrogate(cp rune) bool { return cp >= 0xD800 && cp <= 0xDFFF }

// Pair combines a matched high/low surrogate pair into a scalar rune.
func Pair(high, low rune) rune {
	return 0x10000 + (high-0xD800)<<10 + (low - 0xDC00)
}

// AppendU4 appends the lowercase \uXXXX encoding of cp.
func AppendU4(dst []byte, cp rune) []byte {
	dst = append(dst, '\\', 'u',
		hexdigit[(cp>>12)&0xF], hexdigit[(cp>>8)&0xF],
		hexdigit[(cp>>4)&0xF], hexdigit[cp&0xF])
	return dst
}

// ShortEscape maps the five control characters with short escape forms
// (\b \f \n \r \t) to their escape letter; ok is false otherwise.
func ShortEscape(cp rune) (byte, bool) {
	switch cp {
	case '\b':
		return 'b', true
	case '\f':
		return 'f', true
	case '\n':
		return 'n', true
	case '\r':
		return 'r', true
	case '\t':
		return 't', true
	default:
		return 0, false
	}
}
