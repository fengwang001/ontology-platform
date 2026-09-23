// Package scalar classifies individual Unicode scalar values.
package scalar

// RuneMax is the largest Unicode scalar value.
const RuneMax = 0x10FFFF

const (
	surrLo = 0xD800
	surrHi = 0xDFFF
)

// Valid reports whether r is a Unicode scalar value:
// in range and outside the surrogate area.
func Valid(r rune) bool {
	return r >= 0 && r <= RuneMax && !(r >= surrLo && r <= surrHi)
}

// Surrogate reports whether r lies in the UTF-16 surrogate area.
func Surrogate(r rune) bool { return r >= surrLo && r <= surrHi }

// HighSurrogate reports whether r is a leading (high) surrogate.
func HighSurrogate(r rune) bool { return r >= surrLo && r <= 0xDBFF }

// LowSurrogate reports whether r is a trailing (low) surrogate.
func LowSurrogate(r rune) bool { return r >= 0xDC00 && r <= surrHi }

// CombineSurrogates decodes a surrogate pair into a scalar.
func CombineSurrogates(hi, lo rune) rune {
	return 0x10000 + (hi-surrLo)<<10 + (lo - 0xDC00)
}
