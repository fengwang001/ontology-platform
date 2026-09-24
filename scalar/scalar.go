// Package scalar classifies single Unicode scalar values: valid range,
// surrogates, and replacement. It depends on nothing.
package scalar

const (
	MaxRune     = 0x10FFFF
	Replacement = 0xFFFD
	HiFirst     = 0xD800 // first high surrogate
	HiLast      = 0xDBFF
	LoFirst     = 0xDC00 // first low surrogate
	LoLast      = 0xDFFF
)

// Valid reports whether r is a Unicode scalar value (not a surrogate, in range).
func Valid(r rune) bool { return 0 <= r && r <= MaxRune && !Surrogate(r) }

// Surrogate reports whether r lies in the surrogate area D800..DFFF.
func Surrogate(r rune) bool { return HiFirst <= r && r <= LoLast }

// IsHigh reports whether w is a high surrogate code unit.
func IsHigh(w uint16) bool { return w >= HiFirst && w <= HiLast }

// IsLow reports whether w is a low surrogate code unit.
func IsLow(w uint16) bool { return w >= LoFirst && w <= LoLast }

// Combine joins a surrogate pair into the scalar it encodes.
func Combine(hi, lo uint16) rune {
	return rune(hi-HiFirst)<<10 | rune(lo-LoFirst) + 0x10000
}
