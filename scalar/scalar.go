package scalar

const (
	Replacement = '\uFFFD'
	MaxRune     = '\U0010FFFF'
	SurrogateLo = 0xD800
	SurrogateHi = 0xDFFF
)

func Valid(r rune) bool {
	return r >= 0 && r < SurrogateLo || r > SurrogateHi && r <= MaxRune
}

func Surrogate(x uint16) bool {
	return x >= SurrogateLo && x <= SurrogateHi
}

func HighSurrogate(x uint16) bool {
	return x >= SurrogateLo && x <= 0xDBFF
}

func LowSurrogate(x uint16) bool {
	return x >= 0xDC00 && x <= SurrogateHi
}

func FromPair(hi, lo uint16) rune {
	return 0x10000 + rune(hi-SurrogateLo)<<10 + rune(lo-0xDC00)
}

func ToPair(r rune) (uint16, uint16) {
	r -= 0x10000
	return SurrogateLo + uint16(r>>10), 0xDC00 + uint16(r&0x3FF)
}
