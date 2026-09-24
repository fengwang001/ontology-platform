// Package esc classifies single JSON escape sequences and UTF-16 surrogate
// pairs. It has no dependencies and knows nothing about quoting or I/O.
package esc

// Short maps the byte following a backslash to the byte it denotes.
// It covers \" \\ \/ \b \f \n \r \t; anything else is not a short escape.
func Short(c byte) (byte, bool) {
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

// ShortOf is the inverse of Short restricted to the control characters
// \b \f \n \r \t: it maps a control byte to its short escape letter.
func ShortOf(b byte) (byte, bool) {
	switch b {
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
	}
	return 0, false
}

// HexVal returns the value of one hexadecimal digit (either case).
func HexVal(c byte) (rune, bool) {
	switch {
	case c >= '0' && c <= '9':
		return rune(c - '0'), true
	case c >= 'a' && c <= 'f':
		return rune(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return rune(c-'A') + 10, true
	}
	return 0, false
}

// IsHigh reports whether r is a UTF-16 high surrogate (U+D800-U+DBFF).
func IsHigh(r rune) bool { return r >= 0xD800 && r <= 0xDBFF }

// IsLow reports whether r is a UTF-16 low surrogate (U+DC00-U+DFFF).
func IsLow(r rune) bool { return r >= 0xDC00 && r <= 0xDFFF }

// Combine merges a high and a low surrogate into one code point.
// Callers must ensure IsHigh(hi) and IsLow(lo).
func Combine(hi, lo rune) rune {
	return 0x10000 + ((hi - 0xD800) << 10) + (lo - 0xDC00)
}
