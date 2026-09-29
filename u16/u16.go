// Package u16 decodes and encodes UTF-16LE/UTF-16BE without unicode/utf16.
package u16

import "ontology/scalar"

// Endian selects byte order.
type Endian uint8

const (
	LE Endian = iota
	BE
)

// Kind classifies one decode step.
type Kind uint8

const (
	OK Kind = iota // one scalar (size 2, or 4 for a surrogate pair)
	Invalid        // one orphan surrogate code unit (size 2); reparse after it
	Incomplete     // 1 dangling byte, or a high surrogate with no code unit after
)

// Decode inspects one code point at p[0]. A high surrogate followed by a
// non-low code unit is one invalid unit of 2 bytes; that following unit is
// left for the next call.
func Decode(p []byte, e Endian) (r rune, size int, kind Kind) {
	if len(p) < 2 {
		return 0, len(p), Incomplete
	}
	u := codeUnit(p[0], p[1], e)
	switch {
	case scalar.IsHighSurrogate(rune(u)):
		if len(p) < 4 { // high at the very end may still get a trailing unit
			return 0, len(p), Incomplete
		}
		u2 := codeUnit(p[2], p[3], e)
		if scalar.IsLowSurrogate(rune(u2)) {
			return scalar.FromSurrogatePair(rune(u), rune(u2)), 4, OK
		}
		return 0, 2, Invalid // u2 is reprocessed by the caller
	case scalar.IsLowSurrogate(rune(u)):
		return 0, 2, Invalid
	default:
		return rune(u), 2, OK
	}
}

func codeUnit(a, b byte, e Endian) uint16 {
	if e == LE {
		return uint16(a) | uint16(b)<<8
	}
	return uint16(a)<<8 | uint16(b)
}

// EncLen is the UTF-16 byte length of scalar r.
func EncLen(r rune) int {
	if r >= 0x10000 {
		return 4
	}
	return 2
}

// Append encodes scalar r onto dst as bytes in order e.
func Append(dst []byte, r rune, e Endian) []byte {
	if r >= 0x10000 {
		hi, lo := scalar.SurrogatePair(r)
		dst = appendUnit(dst, uint16(hi), e)
		dst = appendUnit(dst, uint16(lo), e)
	} else {
		dst = appendUnit(dst, uint16(r), e)
	}
	return dst
}

func appendUnit(dst []byte, u uint16, e Endian) []byte {
	if e == LE {
		return append(dst, byte(u), byte(u>>8))
	}
	return append(dst, byte(u>>8), byte(u))
}
