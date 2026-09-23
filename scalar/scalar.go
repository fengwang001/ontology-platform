// Package scalar judges a single Unicode scalar value.
package scalar

// Replacement is U+FFFD, emitted for every illegal unit in replace mode.
const Replacement rune = 0xFFFD

const (
	maxRune   = 0x10FFFF
	surrLo    = 0xD800
	surrHi    = 0xDFFF
	highSurrL = 0xD800
	highSurrH = 0xDBFF
	lowSurrL  = 0xDC00
	lowSurrH  = 0xDFFF
)

// Valid reports whether r is a Unicode scalar value: in range and not a
// surrogate. Over-long encodings and out-of-range code points are rejected
// before reaching r by the encoding packages; this is the final gate.
func Valid(r rune) bool {
	return r >= 0 && r <= maxRune && !Surrogate(r)
}

// Surrogate reports whether r lies in the surrogate area U+D800..U+DFFF.
func Surrogate(r rune) bool { return r >= surrLo && r <= surrHi }

// HighSurrogate reports whether u is a leading surrogate code unit.
func HighSurrogate(u uint16) bool { return u >= highSurrL && u <= highSurrH }

// LowSurrogate reports whether u is a trailing surrogate code unit.
func LowSurrogate(u uint16) bool { return u >= lowSurrL && u <= lowSurrH }

// CombineSurrogates decodes a matched surrogate pair into a scalar.
func CombineSurrogates(hi, lo uint16) rune {
	return rune(uint32(hi)-0xD800)<<10 | rune(uint32(lo)-0xDC00) + 0x10000
}
