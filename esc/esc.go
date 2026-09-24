package esc

func DecodeSimple(c byte) (rune, bool) {
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
	default:
		return 0, false
	}
}

func DecodeHexDigit(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

func IsHighSurrogate(r rune) bool {
	return 0xD800 <= r && r <= 0xDBFF
}

func IsLowSurrogate(r rune) bool {
	return 0xDC00 <= r && r <= 0xDFFF
}

func DecodePair(high, low rune) (rune, bool) {
	if !IsHighSurrogate(high) || !IsLowSurrogate(low) {
		return 0, false
	}
	return 0x10000 + (high-0xD800)<<10 + (low - 0xDC00), true
}
