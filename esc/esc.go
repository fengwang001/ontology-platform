// Package esc classifies single JSON escape sequences and UTF-16
// surrogate pairs. It does not depend on any other package.
package esc

// Simple maps the letter after '\' of a simple escape to its byte.
// The second result reports whether b is a valid simple escape letter.
func Simple(b byte) (byte, bool) {
	switch b {
	case '"':
		return '"', true
	case '\\':
		return '\\', true
	case '/':
		return '/', true
	case 'b':
		return '\b', true
	case 'f':
		return '\f', true
	case 'n':
		return '\n', true
	case 'r':
		return '\r', true
	case 't':
		return '\t', true
	}
	return 0, false
}

// HexVal returns the value of a hexadecimal digit.
func HexVal(b byte) (uint16, bool) {
	switch {
	case '0' <= b && b <= '9':
		return uint16(b - '0'), true
	case 'a' <= b && b <= 'f':
		return uint16(b-'a') + 10, true
	case 'A' <= b && b <= 'F':
		return uint16(b-'A') + 10, true
	}
	return 0, false
}

// IsHighSurrogate reports whether u is a UTF-16 high surrogate (lead).
func IsHighSurrogate(u uint16) bool { return 0xD800 <= u && u <= 0xDBFF }

// IsLowSurrogate reports whether u is a UTF-16 low surrogate (trail).
func IsLowSurrogate(u uint16) bool { return 0xDC00 <= u && u <= 0xDFFF }

// Combine joins a high and a low surrogate into one code point.
// Callers must validate both halves with IsHighSurrogate/IsLowSurrogate.
func Combine(hi, lo uint16) rune {
	return 0x10000 + (rune(hi-0xD800) << 10) + rune(lo-0xDC00)
}
