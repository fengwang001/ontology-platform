package esc

func Simple(b byte) (rune, bool) {
	switch b {
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

func Hex(b byte) (rune, bool) {
	switch {
	case b >= '0' && b <= '9':
		return rune(b - '0'), true
	case b >= 'a' && b <= 'f':
		return rune(b - 'a' + 10), true
	case b >= 'A' && b <= 'F':
		return rune(b - 'A' + 10), true
	default:
		return 0, false
	}
}

func IsHigh(r rune) bool {
	return r >= 0xD800 && r <= 0xDBFF
}

func IsLow(r rune) bool {
	return r >= 0xDC00 && r <= 0xDFFF
}

func Pair(high, low rune) (rune, bool) {
	if !IsHigh(high) || !IsLow(low) {
		return 0, false
	}
	return 0x10000 + (high-0xD800)<<10 + (low - 0xDC00), true
}
