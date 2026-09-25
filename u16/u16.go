// Package u16 decodes and encodes UTF-16LE/BE at byte level.
package u16

import "ontology/scalar"

// Order selects the byte order.
type Order int

const (
	LE Order = iota
	BE
)

// Unit is one decode result over code units.
type Unit struct {
	R          rune
	Size       int  // code units consumed (1 or 2)
	Illegal    bool // one illegal unit of Size code units, R ignored
	Incomplete bool // Size code units are a legal prefix (high surrogate)
}

// Decode decodes one unit from code units cu.
func Decode(cu []uint16) Unit {
	if len(cu) == 0 {
		return Unit{}
	}
	u := cu[0]
	switch {
	case scalar.IsHighSurrogate(u):
		if len(cu) < 2 {
			return Unit{Size: 1, Incomplete: true}
		}
		if scalar.IsLowSurrogate(cu[1]) {
			return Unit{R: scalar.SurrogatePair(u, cu[1]), Size: 2}
		}
		return Unit{Size: 1, Illegal: true} // next unit is reprocessed
	case scalar.IsLowSurrogate(u):
		return Unit{Size: 1, Illegal: true}
	}
	return Unit{R: rune(u), Size: 1}
}

// RuneLen returns the UTF-16 code unit length of a scalar value.
func RuneLen(r rune) int {
	if scalar.NeedsSurrogates(r) {
		return 2
	}
	return 1
}

// Append encodes scalar r onto p in the given byte order.
func Append(p []byte, r rune, o Order) []byte {
	if !scalar.Valid(r) {
		panic("u16.Append: invalid scalar")
	}
	if scalar.NeedsSurrogates(r) {
		hi, lo := scalar.EncodeSurrogate(r)
		p = appendUnit(p, hi, o)
		return appendUnit(p, lo, o)
	}
	return appendUnit(p, uint16(r), o)
}

func appendUnit(p []byte, u uint16, o Order) []byte {
	if o == BE {
		return append(p, byte(u>>8), byte(u))
	}
	return append(p, byte(u), byte(u>>8))
}

// ReadUnit reads one code unit from b (len >= 2) in the given order.
func ReadUnit(b []byte, o Order) uint16 {
	if o == BE {
		return uint16(b[0])<<8 | uint16(b[1])
	}
	return uint16(b[0]) | uint16(b[1])<<8
}
