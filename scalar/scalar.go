package scalar

const (
	MaxRune     = 0x10FFFF
	Replacement = 0xFFFD
)

// Valid reports whether r is a Unicode scalar value
// (not a surrogate and in range).
func Valid(r rune) bool {
	return r >= 0 && r <= MaxRune && !(r >= 0xD800 && r <= 0xDFFF)
}

func HighSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDBFF }
func LowSurrogate(r rune) bool  { return r >= 0xDC00 && r <= 0xDFFF }

// SurrogatePair combines a high and a low UTF-16 code unit into a rune.
func SurrogatePair(hi, lo uint16) rune {
	return (rune(hi)-0xD800)<<10 + (rune(lo) - 0xDC00) + 0x10000
}

// EncodeSurrogatePair splits r (>= 0x10000) into high/low units.
func EncodeSurrogatePair(r rune) (uint16, uint16) {
	r -= 0x10000
	return uint16(0xD800 + (r>>10)&0x3FF), uint16(0xDC00 + r&0x3FF)
}
