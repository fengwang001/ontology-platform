// Package scalar judges a single Unicode scalar value without any decoding
// machinery from the standard library.
package scalar

// RuneMax is the largest valid Unicode scalar value.
const RuneMax = 0x10FFFF

const (
	surrLo = 0xD800
	surrHi = 0xDFFF
)

// Valid reports whether r is a Unicode scalar value: in range and not a
// surrogate.
func Valid(r rune) bool {
	return r >= 0 && r <= RuneMax && !(r >= surrLo && r <= surrHi)
}

// IsSurrogate reports whether r lies in the surrogate area U+D800..U+DFFF.
func IsSurrogate(r rune) bool { return r >= surrLo && r <= surrHi }

// IsHighSurrogate reports a UTF-16 leading surrogate U+D800..U+DBFF.
func IsHighSurrogate(r rune) bool { return r >= surrLo && r <= 0xDBFF }

// IsLowSurrogate reports a UTF-16 trailing surrogate U+DC00..U+DFFF.
func IsLowSurrogate(r rune) bool { return r >= 0xDC00 && r <= surrHi }

// FromSurrogatePair combines a high and a low surrogate into a scalar value.
func FromSurrogatePair(high, low rune) rune {
	return 0x10000 + (high-surrLo)<<10 + (low - 0xDC00)
}

// SurrogatePair splits a scalar >= U+10000 into its UTF-16 code units.
func SurrogatePair(r rune) (high, low rune) {
	r -= 0x10000
	return surrLo + r>>10, 0xDC00 + r&0x3FF
}

// Replacement is emitted for every invalid unit in replace mode.
const Replacement rune = 0xFFFD
