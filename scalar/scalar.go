// Package scalar validates Unicode scalar values without unicode/utf8.
package scalar

// Replacement is U+FFFD.
const Replacement rune = 0xFFFD

// Max is the largest Unicode code point.
const Max = 0x10FFFF

// HighSurrogateStart..LowSurrogateEnd bracket the surrogate range.
const (
	HighSurrogateStart = 0xD800
	LowSurrogateEnd    = 0xDFFF
)

// Valid reports whether r is a Unicode scalar value.
func Valid(r rune) bool {
	return r >= 0 && r <= Max && (r < HighSurrogateStart || r > LowSurrogateEnd)
}

// IsHighSurrogate reports whether u is a UTF-16 high surrogate.
func IsHighSurrogate(u uint16) bool { return u >= 0xD800 && u <= 0xDBFF }

// IsLowSurrogate reports whether u is a UTF-16 low surrogate.
func IsLowSurrogate(u uint16) bool { return u >= 0xDC00 && u <= 0xDFFF }

// SurrogatePair combines a high and low surrogate into a code point.
func SurrogatePair(hi, lo uint16) rune {
	return 0x10000 + (rune(hi)-0xD800)<<10 + (rune(lo) - 0xDC00)
}

// NeedsSurrogates reports whether r encodes as a UTF-16 surrogate pair.
func NeedsSurrogates(r rune) bool { return r >= 0x10000 && r <= Max }

// EncodeSurrogate returns the high/low pair for r (r must need a pair).
func EncodeSurrogate(r rune) (hi, lo uint16) {
	r -= 0x10000
	return 0xD800 + uint16(r>>10), 0xDC00 + uint16(r&0x3FF)
}
