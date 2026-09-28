// Package scalar handles single Unicode scalar values.
package scalar

// Replacement is U+FFFD.
const Replacement rune = 0xFFFD

// Valid reports whether r is a Unicode scalar value.
func Valid(r rune) bool {
	return r >= 0 && r <= 0x10FFFF && !IsSurrogate(r)
}

// IsSurrogate reports whether r lies in the surrogate range.
func IsSurrogate(r rune) bool { return 0xD800 <= r && r <= 0xDFFF }

// IsHighSurrogate reports whether u is a UTF-16 high surrogate.
func IsHighSurrogate(u uint16) bool { return 0xD800 <= u && u <= 0xDBFF }

// IsLowSurrogate reports whether u is a UTF-16 low surrogate.
func IsLowSurrogate(u uint16) bool { return 0xDC00 <= u && u <= 0xDFFF }

// SurrogatePair combines a high and a low surrogate into a scalar.
func SurrogatePair(hi, lo uint16) rune {
	return 0x10000 + (rune(hi)-0xD800)<<10 + (rune(lo) - 0xDC00)
}

// EncodeSurrogatePair splits r (>= 0x10000) into a high/low surrogate pair.
func EncodeSurrogatePair(r rune) (uint16, uint16) {
	v := uint32(r) - 0x10000
	return uint16(v>>10) + 0xD800, uint16(v&0x3FF) + 0xDC00
}

// Contin2 reports whether b is a legal second byte for a UTF-8 lead byte.
// lead is the first byte of a pending multi-byte sequence.
func Contin2(lead, b byte) bool {
	switch {
	case lead == 0xE0:
		return 0xA0 <= b && b <= 0xBF
	case lead == 0xED:
		return 0x80 <= b && b <= 0x9F
	case lead == 0xF0:
		return 0x90 <= b && b <= 0xBF
	case lead == 0xF4:
		return 0x80 <= b && b <= 0x8F
	default:
		return 0x80 <= b && b <= 0xBF
	}
}

// Contin reports whether b is an ordinary continuation byte (80..BF).
func Contin(b byte) bool { return 0x80 <= b && b <= 0xBF }

// LeadLen returns the total sequence length of a UTF-8 lead byte, or 0.
func LeadLen(lead byte) int {
	switch {
	case lead < 0x80:
		return 1
	case 0xC2 <= lead && lead <= 0xDF:
		return 2
	case 0xE0 <= lead && lead <= 0xEF:
		return 3
	case 0xF0 <= lead && lead <= 0xF4:
		return 4
	default:
		return 0
	}
}
