package scalar

const Replacement rune = 0xFFFD

func Valid(r rune) bool {
	return r >= 0 && r <= 0x10FFFF && !IsSurrogate(r)
}

func IsSurrogate(r rune) bool {
	return r >= 0xD800 && r <= 0xDFFF
}

func IsHighSurrogate(r rune) bool {
	return r >= 0xD800 && r <= 0xDBFF
}

func IsLowSurrogate(r rune) bool {
	return r >= 0xDC00 && r <= 0xDFFF
}
