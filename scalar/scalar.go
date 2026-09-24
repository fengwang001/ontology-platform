package scalar

const (
	MaxRune     = 0x10FFFF
	SurrogateLo = 0xD800
	SurrogateHi = 0xDFFF
	Replacement = 0xFFFD
	BOM         = 0xFEFF
)

func Valid(r rune) bool {
	return r >= 0 && r <= MaxRune && !Surrogate(r)
}

func Surrogate(r rune) bool {
	return r >= SurrogateLo && r <= SurrogateHi
}

func HighSurrogate(r rune) bool {
	return r >= SurrogateLo && r <= 0xDBFF
}

func LowSurrogate(r rune) bool {
	return r >= 0xDC00 && r <= SurrogateHi
}
