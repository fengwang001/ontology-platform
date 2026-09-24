// Package esc classifies single JSON escape sequences and combines
// UTF-16 surrogate pairs. It has no dependencies.
package esc

// Simple maps the byte after a backslash to its escaped value.
// It covers \" \\ \/ \b \f \n \r \t but not \u.
func Simple(c byte) (byte, bool) {
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

// EscapeByte maps a rune to the byte following the backslash in its
// minimal escape (\" \\ \b \f \n \r \t); ok is false for other runes.
func EscapeByte(r rune) (byte, bool) {
	switch r {
	case '"', '\\':
		return byte(r), true
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

// IsHighSurrogate reports whether u is a UTF-16 high surrogate (D800–DBFF).
func IsHighSurrogate(u uint16) bool { return 0xD800 <= u && u <= 0xDBFF }

// IsLowSurrogate reports whether u is a UTF-16 low surrogate (DC00–DFFF).
func IsLowSurrogate(u uint16) bool { return 0xDC00 <= u && u <= 0xDFFF }

// Combine joins a high and a low surrogate into one code point.
// ok is false unless hi and lo form a valid pair in the right order.
func Combine(hi, lo uint16) (r rune, ok bool) {
	if !IsHighSurrogate(hi) || !IsLowSurrogate(lo) {
		return 0, false
	}
	return 0x10000 + (rune(hi)-0xD800)<<10 + (rune(lo) - 0xDC00), true
}
