// Package u8 decodes and encodes UTF-8 one scalar at a time, byte level,
// without unicode/utf8. Depends only on scalar.
package u8

import "ontology/scalar"

// LeadInfo classifies a first byte. ok is false for bytes that can never
// start a sequence. For multi-byte leads it returns the total length and
// the inclusive valid range [lo,hi] for the second byte (see DESIGN.md).
func LeadInfo(b byte) (length int, lo, hi byte, ok bool) {
	switch {
	case b < 0x80:
		return 1, 0, 0, true
	case b < 0xC2: // 80..BF stray continuation, C0, C1
		return 0, 0, 0, false
	case b < 0xE0:
		return 2, 0x80, 0xBF, true
	case b == 0xE0:
		return 3, 0xA0, 0xBF, true
	case b < 0xED:
		return 3, 0x80, 0xBF, true
	case b == 0xED:
		return 3, 0x80, 0x9F, true
	case b < 0xF0:
		return 3, 0x80, 0xBF, true
	case b == 0xF0:
		return 4, 0x90, 0xBF, true
	case b < 0xF4:
		return 4, 0x80, 0xBF, true
	case b == 0xF4:
		return 4, 0x80, 0x8F, true
	default: // F5..FF
		return 0, 0, 0, false
	}
}

// Cont reports whether b is a continuation byte (valid at position >= 2).
func Cont(b byte) bool { return b >= 0x80 && b <= 0xBF }

// Value assembles the scalar from a complete validated sequence.
func Value(s []byte) uint32 {
	switch len(s) {
	case 1:
		return uint32(s[0])
	case 2:
		return uint32(s[0]&0x1F)<<6 | uint32(s[1]&0x3F)
	case 3:
		return uint32(s[0]&0x0F)<<12 | uint32(s[1]&0x3F)<<6 | uint32(s[2]&0x3F)
	default:
		return uint32(s[0]&0x07)<<18 | uint32(s[1]&0x3F)<<12 |
			uint32(s[2]&0x3F)<<6 | uint32(s[3]&0x3F)
	}
}

// DecodeUnit decodes one unit from src, applying the maximal-subpart rule.
// Every byte it examines is counted into probed.
func DecodeUnit(src []byte, probed *int64) (val uint32, n int, st scalar.Status) {
	*probed++
	length, lo, hi, ok := LeadInfo(src[0])
	if !ok {
		return 0, 1, scalar.Bad
	}
	if length == 1 {
		return uint32(src[0]), 1, scalar.OK
	}
	if len(src) < 2 {
		return 0, 0, scalar.Short
	}
	*probed++
	if src[1] < lo || src[1] > hi {
		return 0, 1, scalar.Bad
	}
	for i := 2; i < length; i++ {
		if len(src) <= i {
			return 0, 0, scalar.Short
		}
		*probed++
		if !Cont(src[i]) {
			return 0, i, scalar.Bad
		}
	}
	return Value(src[:length]), length, scalar.OK
}

// Size returns the encoded byte length of scalar v (v must be valid).
func Size(v uint32) int {
	switch {
	case v < 0x80:
		return 1
	case v < 0x800:
		return 2
	case v < 0x10000:
		return 3
	default:
		return 4
	}
}

// Encode writes the UTF-8 encoding of v into dst and returns its length.
func Encode(dst []byte, v uint32) int {
	switch n := Size(v); n {
	case 1:
		dst[0] = byte(v)
		return 1
	case 2:
		dst[0] = 0xC0 | byte(v>>6)
		dst[1] = 0x80 | byte(v)&0x3F
		return 2
	case 3:
		dst[0] = 0xE0 | byte(v>>12)
		dst[1] = 0x80 | byte(v>>6)&0x3F
		dst[2] = 0x80 | byte(v)&0x3F
		return 3
	default:
		dst[0] = 0xF0 | byte(v>>18)
		dst[1] = 0x80 | byte(v>>12)&0x3F
		dst[2] = 0x80 | byte(v>>6)&0x3F
		dst[3] = 0x80 | byte(v)&0x3F
		return 4
	}
}
