// Package u8 decodes and encodes UTF-8 byte by byte. It never uses
// unicode/utf8 or any implicit string/rune decoding.
package u8

import "ontology/scalar"

// Kind classifies one decode step.
type Kind uint8

const (
	OK         Kind = iota // size bytes form one legal scalar
	Invalid                // size bytes form exactly one invalid unit
	Incomplete             // p is a valid prefix; more bytes may complete it
)

// BOM is the UTF-8 encoding of U+FEFF.
var BOM = []byte{0xEF, 0xBB, 0xBF}

// Decode inspects one unit beginning at p[0]. For Incomplete it returns the
// whole prefix as size; the caller retries with more bytes appended.
func Decode(p []byte) (r rune, size int, kind Kind) {
	b0 := p[0]
	if b0 < 0x80 {
		return rune(b0), 1, OK
	}
	var n int
	switch {
	case b0 >= 0xC2 && b0 <= 0xDF:
		n = 2
	case b0 == 0xE0 || b0 >= 0xE1 && b0 <= 0xEF:
		n = 3
	case b0 == 0xF0 || b0 >= 0xF1 && b0 <= 0xF4:
		n = 4
	default: // 80..BF stray continuation, C0 C1 (overlong), F5..FF (out of range)
		return 0, 1, Invalid
	}
	for i := 1; i < n; i++ {
		if i >= len(p) {
			return 0, len(p), Incomplete
		}
		b := p[i]
		if !secondOK(b0, i, b) {
			return 0, i + 1, Invalid // swallow lead + matched continuations
		}
	}
	switch n {
	case 2:
		r = rune(b0&0x1F)<<6 | rune(p[1]&0x3F)
	case 3:
		r = rune(b0&0x0F)<<12 | rune(p[1]&0x3F)<<6 | rune(p[2]&0x3F)
	default:
		r = rune(b0&0x07)<<18 | rune(p[1]&0x3F)<<12 |
			rune(p[2]&0x3F)<<6 | rune(p[3]&0x3F)
	}
	return r, n, OK
}

// secondOK applies the lead-specific range to byte position i (i==1 gets the
// special second-byte window that rejects overlong and surrogate encodings).
func secondOK(lead byte, i int, b byte) bool {
	if b < 0x80 || b > 0xBF {
		return false
	}
	if i != 1 {
		return true
	}
	switch lead {
	case 0xE0:
		return b >= 0xA0
	case 0xED:
		return b <= 0x9F
	case 0xF0:
		return b >= 0x90
	case 0xF4:
		return b <= 0x8F
	}
	return true
}

// EncLen is the UTF-8 byte length of scalar r.
func EncLen(r rune) int {
	switch {
	case r < 0x80:
		return 1
	case r < 0x800:
		return 2
	case r < 0x10000:
		return 3
	default:
		return 4
	}
}

// Append encodes scalar r onto dst. r must be a valid scalar value.
func Append(dst []byte, r rune) []byte {
	switch {
	case r < 0x80:
		dst = append(dst, byte(r))
	case r < 0x800:
		dst = append(dst, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
	case r < 0x10000:
		dst = append(dst, 0xE0|byte(r>>12),
			0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	default:
		dst = append(dst, 0xF0|byte(r>>18),
			0x80|byte(r>>12)&0x3F, 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	}
	return dst
}

// Valid reports whether r is encodable (delegates scalar legality).
func Valid(r rune) bool { return scalar.Valid(r) }
