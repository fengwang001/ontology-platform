// Package u8 decodes and encodes UTF-8 one scalar at a time, byte by byte.
package u8

import "ontology/scalar"

// StartLen returns the sequence length for a start byte, or 0 if b can
// never start a sequence (80..BF, C0, C1, F5..FF).
func StartLen(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b < 0xC2:
		return 0
	case b < 0xE0:
		return 2
	case b < 0xF0:
		return 3
	case b < 0xF5:
		return 4
	}
	return 0
}

// ContOK reports whether b is a legal continuation at position pos (1-based)
// of a sequence started by first, applying the second-byte ranges.
func ContOK(first byte, pos int, b byte) bool {
	if pos > 1 {
		return b >= 0x80 && b <= 0xBF
	}
	switch {
	case first < 0xE0: // C2..DF
		return b >= 0x80 && b <= 0xBF
	case first == 0xE0:
		return b >= 0xA0 && b <= 0xBF
	case first < 0xED: // E1..EC
		return b >= 0x80 && b <= 0xBF
	case first == 0xED:
		return b >= 0x80 && b <= 0x9F
	case first < 0xF0: // EE..EF
		return b >= 0x80 && b <= 0xBF
	case first == 0xF0:
		return b >= 0x90 && b <= 0xBF
	case first < 0xF4: // F1..F3
		return b >= 0x80 && b <= 0xBF
	default: // F4
		return b >= 0x80 && b <= 0x8F
	}
}

// Decode decodes one unit at the start of p. On Bad, n is the length of the
// maximal valid prefix (the invalid unit); the offending byte is excluded.
// On Short, p is a valid but incomplete prefix.
func Decode(p []byte) (r int32, n int, st scalar.Status) {
	l := StartLen(p[0])
	if l == 0 {
		return 0, 1, scalar.Bad
	}
	have := len(p)
	if have > l {
		have = l
	}
	for i := 1; i < have; i++ {
		if !ContOK(p[0], i, p[i]) {
			return 0, i, scalar.Bad
		}
	}
	if len(p) < l {
		return 0, 0, scalar.Short
	}
	return Assemble(p), l, scalar.OK
}

// Assemble combines the bytes of a validated sequence into a scalar.
func Assemble(p []byte) int32 {
	switch len(p) {
	case 1:
		return int32(p[0])
	case 2:
		return int32(p[0]&0x1F)<<6 | int32(p[1]&0x3F)
	case 3:
		return int32(p[0]&0x0F)<<12 | int32(p[1]&0x3F)<<6 | int32(p[2]&0x3F)
	}
	return int32(p[0]&0x07)<<18 | int32(p[1]&0x3F)<<12 |
		int32(p[2]&0x3F)<<6 | int32(p[3]&0x3F)
}

// Len returns the encoded length of a valid scalar.
func Len(r int32) int {
	switch {
	case r < 0x80:
		return 1
	case r < 0x800:
		return 2
	case r < 0x10000:
		return 3
	}
	return 4
}

// Append encodes a valid scalar onto dst.
func Append(dst []byte, r int32) []byte {
	switch {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
	case r < 0x10000:
		return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6&0x3F),
			0x80|byte(r&0x3F))
	}
	return append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12&0x3F),
		0x80|byte(r>>6&0x3F), 0x80|byte(r&0x3F))
}
