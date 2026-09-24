package scalar

const (
	Replacement = '\uFFFD'
	MaxRune     = '\U0010FFFF'
	HighMin      rune = 0xD800
	LowMin       rune = 0xDC00
	SurrogateEnd rune = 0xDFFF
)

func IsValid(r rune) bool {
	return r >= 0 && r <= MaxRune && !IsSurrogate(r)
}

func IsSurrogate(r rune) bool { return r >= HighMin && r <= SurrogateEnd }

func IsHighSurrogate(r rune) bool { return r >= HighMin && r < LowMin }

func IsLowSurrogate(r rune) bool { return r >= LowMin && r <= SurrogateEnd }

func SurrogatePair(high, low rune) rune {
	return 0x10000 + (high-HighMin)<<10 + (low - LowMin)
}

func SplitSurrogate(r rune) (rune, rune) {
	r -= 0x10000
	return HighMin + r>>10, LowMin + r&0x3ff
}

func UTF8Len(r rune) int {
	switch {
	case r < 0x80:
		return 1
	case r < 0x800:
		return 2
	case r < 0x10000:
		return 3
	default:
		return 4
	}
}
