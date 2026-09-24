package esc

type Kind uint8

const (
	Invalid Kind = iota
	Simple
	Unicode
)

func Classify(b byte) Kind {
	switch b {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		return Simple
	case 'u':
		return Unicode
	default:
		return Invalid
	}
}

func SimpleValue(b byte) (rune, bool) {
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

func HexValue(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	default:
		return 0, false
	}
}

func IsHighSurrogate(r rune) bool {
	return r >= 0xD800 && r <= 0xDBFF
}

func IsLowSurrogate(r rune) bool {
	return r >= 0xDC00 && r <= 0xDFFF
}

func SurrogatePair(high, low rune) (rune, bool) {
	if !IsHighSurrogate(high) || !IsLowSurrogate(low) {
		return 0, false
	}
	return 0x10000 + (high-0xD800)<<10 + (low - 0xDC00), true
}
