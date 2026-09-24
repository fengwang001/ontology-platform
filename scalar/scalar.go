// Package scalar handles single Unicode scalar values.
package scalar

const (
	MaxValue = 0x10FFFF
	Replacement = 0xFFFD
	BOM = 0xFEFF
	SurrogateHighMin = 0xD800
	SurrogateHighMax = 0xDBFF
	SurrogateLowMin  = 0xDC00
	SurrogateLowMax  = 0xDFFF
)

// Valid reports whether r is a Unicode scalar value.
func Valid(r rune) bool {
	return r >= 0 && r <= MaxValue && !Surrogate(r)
}

// Surrogate reports whether r is in the surrogate block.
func Surrogate(r rune) bool {
	return r >= SurrogateHighMin && r <= SurrogateLowMax
}

// HighSurrogate reports whether u is a leading surrogate code unit.
func HighSurrogate(u uint16) bool {
	return u >= SurrogateHighMin && u <= SurrogateHighMax
}

// LowSurrogate reports whether u is a trailing surrogate code unit.
func LowSurrogate(u uint16) bool {
	return u >= SurrogateLowMin && u <= SurrogateLowMax
}

// FromSurrogatePair decodes a surrogate pair into a scalar.
func FromSurrogatePair(hi, lo uint16) rune {
	return 0x10000 + (rune(hi-SurrogateHighMin) << 10) + rune(lo-SurrogateLowMin)
}

// ToSurrogatePair encodes a scalar above U+FFFF.
func ToSurrogatePair(r rune) (hi, lo uint16) {
	v := uint32(r) - 0x10000
	return SurrogateHighMin + uint16(v>>10), SurrogateLowMin + uint16(v&0x3FF)
}
