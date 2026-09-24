package esc

// SimpleEscape returns the unescaped byte represented by \c.
// It returns false for unknown JSON string escapes and for \u.
func SimpleEscape(c byte) (byte, bool) {
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
	default:
		return 0, false
	}
}

// IsHexDigit reports whether c is a valid Unicode hexadecimal digit.
func IsHexDigit(c byte) bool {
	return HexValue(c) >= 0
}

// HexValue returns a hexadecimal digit's value, or -1 when it is invalid.
func HexValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	default:
		return -1
	}
}

// HexCode parses exactly four hexadecimal digits and returns a UTF-16 code unit.
// The digits must have length four.
func HexCode(digits []byte) (rune, bool) {
	if len(digits) != 4 {
		return 0, false
	}
	value := 0
	for _, digit := range digits {
		nibble := HexValue(digit)
		if nibble < 0 {
			return 0, false
		}
		value = value<<4 | nibble
	}
	return rune(value), true
}

// IsHighSurrogate reports whether code is in the UTF-16 high-surrogate range.
func IsHighSurrogate(code rune) bool {
	return code >= 0xD800 && code <= 0xDBFF
}

// IsLowSurrogate reports whether code is in the UTF-16 low-surrogate range.
func IsLowSurrogate(code rune) bool {
	return code >= 0xDC00 && code <= 0xDFFF
}

// SurrogatePair combines a valid high and low UTF-16 surrogate.
func SurrogatePair(high, low rune) (rune, bool) {
	if !IsHighSurrogate(high) || !IsLowSurrogate(low) {
		return 0, false
	}
	return 0x10000 + (high-0xD800)<<10 + (low - 0xDC00), true
}
