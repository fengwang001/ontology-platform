package scalar

const (
	Replacement = '\uFFFD'
	BOM         = '\uFEFF'
)

func IsScalar(r rune) bool {
	return r >= 0 && r <= 0x10FFFF && !(r >= 0xD800 && r <= 0xDFFF)
}

func IsHighSurrogate(r rune) bool {
	return r >= 0xD800 && r <= 0xDBFF
}

func IsLowSurrogate(r rune) bool {
	return r >= 0xDC00 && r <= 0xDFFF
}

func FromSurrogates(high, low rune) rune {
	return 0x10000 + (high-0xD800)<<10 + (low - 0xDC00)
}
