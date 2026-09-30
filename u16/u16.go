// Package u16 decodes and encodes UTF-16LE/BE one unit at a time,
// byte level, without unicode/utf16. Depends only on scalar.
package u16

import "ontology/scalar"

// Order is a UTF-16 byte order.
type Order int

const (
	LE Order = iota
	BE
)

const (
	hiBase = 0xD800
	loBase = 0xDC00
)

// ReadWord assembles one 16-bit code unit from two bytes.
func ReadWord(b0, b1 byte, o Order) uint16 {
	if o == BE {
		return uint16(b0)<<8 | uint16(b1)
	}
	return uint16(b1)<<8 | uint16(b0)
}

// DecodeUnit decodes one unit from src. A lone surrogate is an invalid
// unit of 2 bytes; the following code unit is never swallowed. Every
// examined byte is counted into probed.
func DecodeUnit(src []byte, o Order, probed *int64) (val uint32, n int, st scalar.Status) {
	if len(src) < 2 {
		return 0, 0, scalar.Short
	}
	*probed += 2
	w := uint32(ReadWord(src[0], src[1], o))
	switch {
	case scalar.IsLow(w):
		return 0, 2, scalar.Bad
	case !scalar.IsHigh(w):
		return w, 2, scalar.OK
	}
	if len(src) < 4 {
		return 0, 0, scalar.Short
	}
	*probed += 2
	w2 := uint32(ReadWord(src[2], src[3], o))
	if !scalar.IsLow(w2) {
		return 0, 2, scalar.Bad
	}
	return scalar.Combine(w, w2), 4, scalar.OK
}

// Size returns the encoded byte length of scalar v (v must be valid).
func Size(v uint32) int {
	if v < 0x10000 {
		return 2
	}
	return 4
}

// Encode writes the UTF-16 encoding of v into dst and returns its length.
// Scalars above U+FFFF become a surrogate pair.
func Encode(dst []byte, v uint32, o Order) int {
	if v < 0x10000 {
		putWord(dst, uint16(v), o)
		return 2
	}
	v -= 0x10000
	putWord(dst, uint16(hiBase+(v>>10)), o)
	putWord(dst[2:], uint16(loBase+(v&0x3FF)), o)
	return 4
}

func putWord(dst []byte, w uint16, o Order) {
	if o == BE {
		dst[0], dst[1] = byte(w>>8), byte(w)
	} else {
		dst[0], dst[1] = byte(w), byte(w>>8)
	}
}
