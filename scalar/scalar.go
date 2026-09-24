// Package scalar classifies a single Unicode scalar value.
package scalar

const (
	MaxRune     = 0x10FFFF // highest legal code point
	SurrogateLo = 0xD800   // high-surrogate range start
	SurrogateHi = 0xDFFF   // low-surrogate range end
	Replacement = 0xFFFD   // U+FFFD
	BOM         = 0xFEFF
)

// Valid reports whether r is a Unicode scalar value: in range and not a
// surrogate. Out-of-range values (including overlong/non-shortest results)
// are rejected here.
func Valid(r rune) bool {
	return r >= 0 && r <= MaxRune && !(r >= SurrogateLo && r <= SurrogateHi)
}

// IsHighSurrogate reports whether u is a UTF-16 high surrogate.
func IsHighSurrogate(u uint16) bool { return u >= 0xD800 && u <= 0xDBFF }

// IsLowSurrogate reports whether u is a UTF-16 low surrogate.
func IsLowSurrogate(u uint16) bool { return u >= 0xDC00 && u <= 0xDFFF }

// CombineSurrogates decodes a high/low surrogate pair into a scalar.
func CombineSurrogates(hi, lo uint16) rune {
	return 0x10000 + (rune(hi)-0xD800)<<10 + (rune(lo) - 0xDC00)
}
