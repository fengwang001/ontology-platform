package scalar

const (
	MaxRune        = 0x10FFFF
	Replacement    = 0xFFFD
	HighMin        = 0xD800
	HighMax        = 0xDBFF
	LowMin         = 0xDC00
	LowMax         = 0xDFFF
	SurrMax        = 0xDFFF
	SurrogateBase  = 0x10000
	SurrogateShift = 10
	SurrMask       = 0x3FF
)

func IsScalar(r rune) bool {
	return r >= 0 && r <= MaxRune && !IsSurrogate(r)
}

func IsSurrogate(r rune) bool {
	return r >= HighMin && r <= SurrMax
}

func IsHighSurrogate(r rune) bool {
	return r >= HighMin && r <= HighMax
}

func IsLowSurrogate(r rune) bool {
	return r >= LowMin && r <= LowMax
}

func DecodeSurrogate(high, low rune) (rune, bool) {
	if !IsHighSurrogate(high) || !IsLowSurrogate(low) {
		return Replacement, false
	}
	return SurrogateBase + (high-HighMin)<<SurrogateShift + (low - LowMin), true
}

func EncodeSurrogate(r rune) (high, low rune, ok bool) {
	if r < SurrogateBase || r > MaxRune {
		return 0, 0, false
	}
	value := r - SurrogateBase
	return HighMin + value>>SurrogateShift, LowMin + value&SurrMask, true
}
