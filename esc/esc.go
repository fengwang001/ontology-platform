package esc

// SimpleDecode decodes one of the eight JSON escapes that do not use \u.
func SimpleDecode(c byte) (rune, bool) {
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
	default:
		return 0, false
	}
}

// HexValue converts one ASCII hexadecimal digit to its numeric value.
func HexValue(c byte) (rune, bool) {
	switch {
	case c >= '0' && c <= '9':
		return rune(c - '0'), true
	case c >= 'a' && c <= 'f':
		return rune(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return rune(c-'A') + 10, true
	default:
		return 0, false
	}
}

// IsHighSurrogate reports whether code is a UTF-16 high surrogate.
func IsHighSurrogate(code rune) bool {
	return code >= 0xD800 && code <= 0xDBFF
}

// IsLowSurrogate reports whether code is a UTF-16 low surrogate.
func IsLowSurrogate(code rune) bool {
	return code >= 0xDC00 && code <= 0xDFFF
}

// Pair combines a validated high and low surrogate into a Unicode code point.
func Pair(high, low rune) rune {
	return 0x10000 + (high-0xD800)<<10 + (low - 0xDC00)
}

// AppendHex appends code as exactly four uppercase-free lowercase hex digits.
func AppendHex(dst []byte, code rune) []byte {
	const digits = "0123456789abcdef"
	return append(dst,
		'\\', 'u',
		digits[byte(code>>12)&0xF],
		digits[byte(code>>8)&0xF],
		digits[byte(code>>4)&0xF],
		digits[byte(code)&0xF],
	)
}
