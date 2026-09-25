// Package scalar defines validity rules for a single Unicode scalar value.
// It has no dependencies and does no byte-level decoding itself.
package scalar

const (
	// Max is the largest legal Unicode scalar value.
	Max = 0x10FFFF
	// Replacement is U+FFFD, emitted per invalid unit in replace mode.
	Replacement = 0xFFFD
	// BOM is U+FEFF, recognized only at the very start of a stream.
	BOM = 0xFEFF

	surrogateLo = 0xD800
	surrogateHi = 0xDFFF
)

// Valid reports whether cp is a legal Unicode scalar value: inside
// [0, Max] and outside the surrogate range.
func Valid(cp rune) bool {
	return cp >= 0 && cp <= Max && !IsSurrogate(cp)
}

// IsSurrogate reports whether cp lies in the surrogate range D800..DFFF,
// which can never appear in a well-formed UTF-8/UTF-16 scalar stream.
func IsSurrogate(cp rune) bool {
	return cp >= surrogateLo && cp <= surrogateHi
}

// MinUTF8Len returns the shortest UTF-8 encoding length for cp.
// An encoded sequence shorter than this is a non-shortest (overlong)
// form and therefore invalid.
func MinUTF8Len(cp rune) int {
	switch {
	case cp < 0x80:
		return 1
	case cp < 0x800:
		return 2
	case cp < 0x10000:
		return 3
	default:
		return 4
	}
}
