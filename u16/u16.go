// Package u16 decodes and encodes UTF-16LE/UTF-16BE byte streams
// scalar by scalar without unicode/utf16.
package u16

import "ontology/scalar"

const (
	NeedMore      = 0
	RuneReady     = 1
	InvalidByte   = 2 // the complete unit just fed is invalid on its own
	InvalidPrefix = 3 // buffered high surrogate is invalid; fed unit NOT consumed
)

// Decoder is a byte-at-a-time UTF-16 decoder for one fixed byte order.
// Not safe for concurrent use.
type Decoder struct {
	little bool
	half   int // -1: none; else first byte of a code unit
	haveHi bool
	hi     uint16
	val    rune
}

func NewDecoder(littleEndian bool) *Decoder { return &Decoder{little: littleEndian, half: -1} }

func (d *Decoder) Reset()     { d.half, d.haveHi = -1, false }
func (d *Decoder) Rune() rune { return d.val }
func (d *Decoder) PendingLen() int {
	n := 0
	if d.half >= 0 {
		n++
	}
	if d.haveHi {
		n += 2
	}
	return n
}

// Push feeds one byte. Invalid units are always one code unit (2 bytes).
func (d *Decoder) Push(b byte) int {
	if d.half < 0 {
		d.half = int(b)
		return NeedMore
	}
	var u uint16
	if d.little {
		u = uint16(d.half) | uint16(b)<<8
	} else {
		u = uint16(d.half)<<8 | uint16(b)
	}
	d.half = -1
	if !d.haveHi {
		switch {
		case scalar.HighSurrogate(rune(u)):
			d.hi, d.haveHi = u, true
			return NeedMore
		case scalar.LowSurrogate(rune(u)):
			return InvalidByte
		default:
			d.val = rune(u)
			return RuneReady
		}
	}
	if scalar.LowSurrogate(rune(u)) {
		d.haveHi = false
		d.val = scalar.SurrogatePair(d.hi, u)
		return RuneReady
	}
	return InvalidPrefix
}

func put16(dst []byte, u uint16, little bool) []byte {
	if little {
		return append(dst, byte(u), byte(u>>8))
	}
	return append(dst, byte(u>>8), byte(u))
}

// Encode appends the UTF-16 (LE if little) encoding of r to dst.
func Encode(dst []byte, r rune, little bool) []byte {
	if r < 0x10000 {
		return put16(dst, uint16(r), little)
	}
	hi, lo := scalar.EncodeSurrogatePair(r)
	dst = put16(dst, hi, little)
	return put16(dst, lo, little)
}
