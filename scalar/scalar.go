// Package scalar classifies individual Unicode scalar values.
package scalar

// Rune is a Unicode code point held as an unsigned 21-bit value.
type Rune = uint32

const (
	MaxRune    Rune = 0x10FFFF
	SurrogateLo Rune = 0xD800
	SurrogateHi Rune = 0xDFFF
	Replacement Rune = 0xFFFD
	BOM         Rune = 0xFEFF
)

// InRange reports whether r is within the Unicode code-point space.
func InRange(r Rune) bool { return r <= MaxRune }

// IsSurrogate reports whether r lies in the surrogate area.
func IsSurrogate(r Rune) bool { return SurrogateLo <= r && r <= SurrogateHi }

// IsScalar reports whether r is a Unicode scalar value.
func IsScalar(r Rune) bool { return InRange(r) && !IsSurrogate(r) }

// IsHighSurrogate reports whether r is a leading surrogate.
func IsHighSurrogate(r Rune) bool { return 0xD800 <= r && r <= 0xDBFF }

// IsLowSurrogate reports whether r is a trailing surrogate.
func IsLowSurrogate(r Rune) bool { return 0xDC00 <= r && r <= 0xDFFF }

// JoinSurrogates combines a matched surrogate pair into a scalar value.
func JoinSurrogates(hi, lo Rune) Rune {
	return 0x10000 + (hi-0xD800)<<10 + (lo - 0xDC00)
}

// SplitSurrogate encodes r (>= 0x10000) into a high/low surrogate pair.
func SplitSurrogate(r Rune) (hi, lo Rune) {
	r -= 0x10000
	return 0xD800 + r>>10, 0xDC00 + r&0x3FF
}
