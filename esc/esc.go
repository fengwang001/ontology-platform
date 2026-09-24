// Package esc classifies single JSON escape sequences and UTF-16 surrogates.
package esc

// Simple maps the byte after a backslash to its rune for the two-character
// escapes \" \\ \/ \b \f \n \r \t. ok is false for anything else.
func Simple(c byte) (r rune, ok bool) {
	switch c {
	case '"', '\\', '/':
		return rune(c), true
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

// HexVal returns the value of a hexadecimal digit, or -1 if c is not one.
func HexVal(c byte) int {
	switch {
	case '0' <= c && c <= '9':
		return int(c - '0')
	case 'a' <= c && c <= 'f':
		return int(c-'a') + 10
	case 'A' <= c && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// IsHigh reports whether v is a UTF-16 high surrogate (U+D800–U+DBFF).
func IsHigh(v rune) bool { return 0xD800 <= v && v <= 0xDBFF }

// IsLow reports whether v is a UTF-16 low surrogate (U+DC00–U+DFFF).
func IsLow(v rune) bool { return 0xDC00 <= v && v <= 0xDFFF }

// Combine joins a high and a low surrogate into one code point.
// Callers must guarantee IsHigh(hi) and IsLow(lo).
func Combine(hi, lo rune) rune { return 0x10000 + (hi-0xD800)<<10 + (lo - 0xDC00) }
