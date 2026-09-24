// Package esc classifies single JSON escape sequences and UTF-16
// surrogate code units. It depends on nothing.
package esc

// Simple maps an escaped letter to the byte it denotes; ok is false
// for 'u' (handled separately as XXXX) and for non-escape letters.
func Simple(c byte) (b byte, ok bool) {
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

// Hex returns the value of a hexadecimal digit; ok is false otherwise.
func Hex(c byte) (v uint16, ok bool) {
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

// IsHigh reports whether u is a high surrogate (U+D800–U+DBFF).
func IsHigh(u uint16) bool { return 0xD800 <= u && u <= 0xDBFF }

// IsLow reports whether u is a low surrogate (U+DC00–U+DFFF).
func IsLow(u uint16) bool { return 0xDC00 <= u && u <= 0xDFFF }

// Combine joins a validated surrogate pair into a single code point.
func Combine(hi, lo uint16) rune {
	return 0x10000 + (rune(hi)-0xD800)<<10 + rune(lo) - 0xDC00
}
