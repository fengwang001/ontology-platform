package scalar

func Valid(r rune) bool {
	return r >= 0 && r <= 0x10ffff && !Surrogate(r)
}

func Surrogate(r rune) bool {
	return r >= 0xd800 && r <= 0xdfff
}

func HighSurrogate(r rune) bool {
	return r >= 0xd800 && r <= 0xdbff
}

func LowSurrogate(r rune) bool {
	return r >= 0xdc00 && r <= 0xdfff
}
