// Package esc classifies single JSON string escape sequences and
// validates UTF-16 surrogate pair combinations. It has no dependencies.
package esc

// Simple maps an escape letter to the byte it denotes, covering
// \" \\ \/ \b \f \n \r \t; ok is false for any other letter.
func Simple(c byte) (v byte, ok bool) {
	switch c {
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

// Hex returns the value of a hexadecimal digit; ok is false for non-digits.
func Hex(c byte) (uint16, bool) {
	switch {
	case '0' <= c && c <= '9':
		return uint16(c - '0'), true
	case 'a' <= c && c <= 'f':
		return uint16(c-'a') + 10, true
	case 'A' <= c && c <= 'F':
		return uint16(c-'A') + 10, true
	}
	return 0, false
}

// IsHigh reports whether u is a UTF-16 high surrogate (lead).
func IsHigh(u uint16) bool { return 0xD800 <= u && u <= 0xDBFF }

// IsLow reports whether u is a UTF-16 low surrogate (trail).
func IsLow(u uint16) bool { return 0xDC00 <= u && u <= 0xDFFF }

// Combine joins a validated surrogate pair into its code point.
// Callers must guarantee IsHigh(hi) && IsLow(lo).
func Combine(hi, lo uint16) rune {
	return 0x10000 + (rune(hi)-0xD800)<<10 + (rune(lo) - 0xDC00)
}
