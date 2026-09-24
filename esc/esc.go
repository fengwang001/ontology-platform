// Package esc classifies single JSON escape sequences and validates
// UTF-16 surrogate pairs. It has no dependencies.
package esc

// Simple maps the letter of a one-character escape (the byte after '\')
// to the byte it denotes. ok is false for any other letter, including 'u'.
func Simple(letter byte) (b byte, ok bool) {
	switch letter {
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

// ShortOf maps a control character to the letter of its short escape
// form (\b \f \n \r \t). ok is false when no short form exists.
func ShortOf(c byte) (letter byte, ok bool) {
	switch c {
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

// HexDigit parses one hexadecimal digit into its 4-bit value.
func HexDigit(b byte) (v uint16, ok bool) {
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

// Hex4 parses exactly four hexadecimal digits into a 16-bit code unit.
func Hex4(d []byte) (u uint16, ok bool) {
	if len(d) != 4 {
		return 0, false
	}
	for _, b := range d {
		v, ok := HexDigit(b)
		if !ok {
			return 0, false
		}
		u = u<<4 | v
	}
	return u, true
}

// IsHigh reports whether u is a high surrogate (U+D800–U+DBFF).
func IsHigh(u uint16) bool { return 0xD800 <= u && u <= 0xDBFF }

// IsLow reports whether u is a low surrogate (U+DC00–U+DFFF).
func IsLow(u uint16) bool { return 0xDC00 <= u && u <= 0xDFFF }

// Combine joins a high and a low surrogate into one code point.
// ok is false unless hi and lo are high/low surrogates respectively.
func Combine(hi, lo uint16) (r rune, ok bool) {
	if !IsHigh(hi) || !IsLow(lo) {
		return 0, false
	}
	return 0x10000 + (rune(hi-0xD800) << 10) + rune(lo-0xDC00), true
}
