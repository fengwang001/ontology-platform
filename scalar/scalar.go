// Package scalar validates individual Unicode scalar values without the
// unicode/utf8 or unicode/utf16 packages.
package scalar

// Rune is a Unicode code point.
type Rune = rune

// Replacement is U+FFFD, emitted for every illegal unit in replace mode.
const Replacement Rune = 0xFFFD

// BOM is U+FEFF.
const BOM Rune = 0xFEFF

// MaxScalar is the largest legal Unicode scalar value.
const MaxScalar Rune = 0x10FFFF

const (
	surrLo Rune = 0xD800
	surrHi Rune = 0xDFFF
)

// Surrogate reports whether r lies in the surrogate range D800..DFFF.
func Surrogate(r Rune) bool { return r >= surrLo && r <= surrHi }

// Scalar reports whether r is a Unicode scalar value: in range and not a
// surrogate.
func Scalar(r Rune) bool { return r >= 0 && r <= MaxScalar && !Surrogate(r) }

// HiSurrogate reports whether u is a UTF-16 high surrogate D800..DBFF.
func HiSurrogate(u uint16) bool { return u >= 0xD800 && u <= 0xDBFF }

// LoSurrogate reports whether u is a UTF-16 low surrogate DC00..DFFF.
func LoSurrogate(u uint16) bool { return u >= 0xDC00 && u <= 0xDFFF }

// SurrogatePair combines a high and a low surrogate into a scalar.
func SurrogatePair(hi, lo uint16) Rune {
	return 0x10000 + (Rune(hi-0xD800) << 10) + Rune(lo-0xDC00)
}

// SplitPair encodes a supplementary scalar into its surrogate pair.
func SplitPair(r Rune) (hi, lo uint16) {
	r -= 0x10000
	return uint16(0xD800 + (r >> 10)), uint16(0xDC00 + (r & 0x3FF))
}
