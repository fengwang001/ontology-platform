// Package esc classifies and validates single JSON escape sequences.
package esc

// SimpleEscape reports whether b is a legal single-character JSON escape
// after a backslash (one of " \ / b f n r t).
func SimpleEscape(b byte) bool {
	switch b {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		return true
	}
	return false
}

// HexVal returns the hex value of b or ok=false if b is not a hex digit.
func HexVal(b byte) (v rune, ok bool) {
	switch {
	case b >= '0' && b <= '9':
		return rune(b - '0'), true
	case b >= 'a' && b <= 'f':
		return rune(b-'a') + 10, true
	case b >= 'A' && b <= 'F':
		return rune(b-'A') + 10, true
	}
	return 0, false
}

// IsHighSurrogate reports whether r is a UTF-16 high surrogate.
func IsHighSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDBFF }

// IsLowSurrogate reports whether r is a UTF-16 low surrogate.
func IsLowSurrogate(r rune) bool { return r >= 0xDC00 && r <= 0xDFFF }

// SurrogatePair combines a high and low surrogate into one code point.
func SurrogatePair(hi, lo rune) rune {
	return 0x10000 + (hi-0xD800)<<10 + (lo - 0xDC00)
}
