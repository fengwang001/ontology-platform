// Package scalar classifies individual Unicode scalar values.
package scalar

// IsScalar reports whether r is a Unicode scalar value: [0,0xD7FF] or
// [0xE000,0x10FFFF].
func IsScalar(r rune) bool {
	return r >= 0 && r <= 0x10FFFF && !IsSurrogate(r)
}

// IsSurrogate reports whether r lies in the surrogate area [0xD800,0xDFFF].
func IsSurrogate(r rune) bool { return uint32(r)-0xD800 <= 0x7FF }

// IsHighSurrogate reports whether r is a high surrogate [0xD800,0xDBFF].
func IsHighSurrogate(r rune) bool { return uint32(r)-0xD800 <= 0x3FF }

// IsLowSurrogate reports whether r is a low surrogate [0xDC00,0xDFFF].
func IsLowSurrogate(r rune) bool { return uint32(r)-0xDC00 <= 0x3FF }

// SurrogatePair combines a high and a low surrogate into a scalar value.
func SurrogatePair(high, low rune) rune {
	return 0x10000 + ((high - 0xD800) << 10) + (low - 0xDC00)
}

// SplitSurrogate encodes r (>= 0x10000) into its high/low surrogate pair.
func SplitSurrogate(r rune) (high, low rune) {
	r -= 0x10000
	return 0xD800 + (r >> 10), 0xDC00 + (r & 0x3FF)
}

// Replacement is U+FFFD.
const Replacement rune = 0xFFFD

// BOM is U+FEFF.
const BOM rune = 0xFEFF
