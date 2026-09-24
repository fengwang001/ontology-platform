// Package esc classifies the escape sequences allowed inside a JSON
// string literal (RFC 8259) and validates UTF-16 surrogate code units.
// It has no dependencies.
package esc

// Simple maps an escape letter to the byte it denotes. The ok result is
// false for any byte that may not follow a backslash (e.g. 'x', '\'', 'u'
// is handled separately because it needs four hex digits).
func Simple(c byte) (v byte, ok bool) {
	switch c {
	case '"', '\\', '/':
		return c, true
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

// HexDigit returns the value of an ASCII hex digit; ok is false for any
// other byte.
func HexDigit(c byte) (v int, ok bool) {
	switch {
	case '0' <= c && c <= '9':
		return int(c - '0'), true
	case 'a' <= c && c <= 'f':
		return int(c-'a') + 10, true
	case 'A' <= c && c <= 'F':
		return int(c-'A') + 10, true
	}
	return 0, false
}

// IsHigh reports whether u is a UTF-16 high surrogate (U+D800–U+DBFF).
func IsHigh(u uint16) bool { return 0xD800 <= u && u <= 0xDBFF }

// IsLow reports whether u is a UTF-16 low surrogate (U+DC00–U+DFFF).
func IsLow(u uint16) bool { return 0xDC00 <= u && u <= 0xDFFF }

// Combine merges a valid high/low surrogate pair into its code point
// (U+10000–U+10FFFF). Callers must check IsHigh and IsLow first.
func Combine(hi, lo uint16) rune {
	return 0x10000 + (rune(hi)-0xD800)<<10 + (rune(lo) - 0xDC00)
}
