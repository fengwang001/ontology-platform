package scalar

const (
	Replacement = '\uFFFD'
	MaxRune     = '\U0010FFFF'
)

func IsScalar(r rune) bool {
	return r >= 0 && r <= 0xD7FF || r >= 0xE000 && r <= MaxRune
}

func IsHighSurrogate(u uint16) bool {
	return u >= 0xD800 && u <= 0xDBFF
}

func IsLowSurrogate(u uint16) bool {
	return u >= 0xDC00 && u <= 0xDFFF
}

func DecodeSurrogate(high, low uint16) (rune, bool) {
	if !IsHighSurrogate(high) || !IsLowSurrogate(low) {
		return 0, false
	}
	return rune(high-0xD800)<<10 + rune(low-0xDC00) + 0x10000, true
}

func EncodeSurrogate(r rune) (high, low uint16, ok bool) {
	if r < 0x10000 || r > MaxRune {
		return 0, 0, false
	}
	r -= 0x10000
	return uint16(r>>10) + 0xD800, uint16(r&0x3FF) + 0xDC00, true
}
