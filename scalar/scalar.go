package scalar

type Rune uint32

const (
	Replacement Rune = 0xFFFD
	BOM         Rune = 0xFEFF
	MaxRune     Rune = 0x10FFFF
	HighMin     Rune = 0xD800
	LowMax      Rune = 0xDFFF
)

func IsScalar(r Rune) bool {
	return r <= MaxRune && (r < HighMin || r > LowMax)
}

func IsHighSurrogate(r Rune) bool { return r >= HighMin && r < 0xDC00 }
func IsLowSurrogate(r Rune) bool  { return r >= 0xDC00 && r <= LowMax }
func IsSurrogate(r Rune) bool     { return IsHighSurrogate(r) || IsLowSurrogate(r) }
