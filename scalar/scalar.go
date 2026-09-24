package scalar

const (
	MaxRune     rune = 0x10FFFF
	Replacement rune = '\uFFFD'
)

func Valid(r rune) bool {
	return r >= 0 && r <= MaxRune && !Surrogate(r)
}

func Surrogate(r rune) bool {
	return r >= 0xD800 && r <= 0xDFFF
}

func HighSurrogate(v uint16) bool {
	return v >= 0xD800 && v <= 0xDBFF
}

func LowSurrogate(v uint16) bool {
	return v >= 0xDC00 && v <= 0xDFFF
}
