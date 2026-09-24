package scalar

const (
	Replacement rune = '\uFFFD'
	ByteOrderMark rune = '\uFEFF'
)

func Valid(r rune) bool {
	return r >= 0 && r <= 0x10FFFF && !Surrogate(r)
}

func Surrogate(r rune) bool {
	return r >= 0xD800 && r <= 0xDFFF
}

func HighSurrogate(r rune) bool {
	return r >= 0xD800 && r <= 0xDBFF
}

func LowSurrogate(r rune) bool {
	return r >= 0xDC00 && r <= 0xDFFF
}

func SurrogatePair(high, low rune) (rune, bool) {
	if !HighSurrogate(high) || !LowSurrogate(low) {
		return 0, false
	}
	return 0x10000 + (high-0xD800)<<10 + (low - 0xDC00), true
}

func EncodeSurrogatePair(r rune) (high, low rune, ok bool) {
	if r < 0x10000 || r > 0x10FFFF {
		return 0, 0, false
	}
	r -= 0x10000
	return 0xD800 + r>>10, 0xDC00 + r&0x3FF, true
}
