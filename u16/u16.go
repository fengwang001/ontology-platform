// Package u16 decodes and encodes UTF-16 (LE and BE) one scalar at a time.
package u16

import "ontology/scalar"

// Order selects the byte order of UTF-16 code units.
type Order bool

const (
	BE Order = false
	LE Order = true
)

// Word reads one 16-bit code unit from p (len(p) >= 2).
func (o Order) Word(p []byte) uint16 {
	if o == LE {
		return uint16(p[0]) | uint16(p[1])<<8
	}
	return uint16(p[0])<<8 | uint16(p[1])
}

// AppendWord appends one 16-bit code unit to dst.
func (o Order) AppendWord(dst []byte, w uint16) []byte {
	if o == LE {
		return append(dst, byte(w), byte(w>>8))
	}
	return append(dst, byte(w>>8), byte(w))
}

// Decode decodes one unit at the start of p. An isolated surrogate is Bad
// with n == 2 (only the surrogate itself; the next word is reprocessed).
// Short means p ends inside a unit or after a high surrogate.
func Decode(p []byte, o Order) (r int32, n int, st scalar.Status) {
	if len(p) < 2 {
		return 0, 0, scalar.Short
	}
	w1 := o.Word(p)
	if scalar.IsHi(w1) {
		if len(p) < 4 {
			return 0, 0, scalar.Short
		}
		w2 := o.Word(p[2:])
		if !scalar.IsLo(w2) {
			return 0, 2, scalar.Bad
		}
		return scalar.Combine(w1, w2), 4, scalar.OK
	}
	if scalar.IsLo(w1) {
		return 0, 2, scalar.Bad
	}
	return int32(w1), 2, scalar.OK
}

// Len returns the encoded length of a valid scalar in code units bytes.
func Len(r int32) int {
	if r < 0x10000 {
		return 2
	}
	return 4
}

// Append encodes a valid scalar onto dst, using a surrogate pair for
// values at or above U+10000.
func Append(dst []byte, r int32, o Order) []byte {
	if r < 0x10000 {
		return o.AppendWord(dst, uint16(r))
	}
	r -= 0x10000
	dst = o.AppendWord(dst, uint16(scalar.HiMin+(r>>10)))
	return o.AppendWord(dst, uint16(scalar.LoMin+(r&0x3FF)))
}
