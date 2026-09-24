// Package u16 decodes and encodes UTF-16LE/UTF-16BE one byte at a time.
package u16

import "ontology/scalar"

// Byte orders.
const (
	LE = iota
	BE
)

// Token kinds.
const (
	OK   = iota // one valid scalar
	Bad         // one invalid unit
	Need        // incomplete unit/pair
)

// Token is one decoded unit. Len counts the bytes consumed (2 or 4); BOM
// marks a recognized leading byte-order mark.
type Token struct {
	R      rune
	Kind   int
	Offset int
	Len    int
	BOM    bool
}

// Decoder consumes one byte per Step; order may be auto-detected from a
// leading FEFF/FFFE unit.
type Decoder struct {
	order   int
	auto    bool
	odd     byte
	hasOdd  bool
	hi      uint16
	hasHi   bool
	hiOff   int
	nextOff int

	// token produced for the unit right after a bad high surrogate
	extra    Token
	hasExtra bool
}

// NewDecoder builds a decoder. orderAuto picks order from a leading BOM.
func NewDecoder(order int, orderAuto bool) *Decoder {
	return &Decoder{order: order, auto: orderAuto}
}

// SetStart sets the absolute offset of the first input byte.
func (d *Decoder) SetStart(off int) { d.nextOff = off }

func (d *Decoder) unit(a, b byte) uint16 {
	if d.order == BE {
		return uint16(a)<<8 | uint16(b)
	}
	return uint16(b)<<8 | uint16(a)
}

// Step feeds one byte. A Bad token never silently eats unrelated bytes:
// after a bad high surrogate the following complete unit is parked.
func (d *Decoder) Step(b byte) Token {
	if !d.hasOdd {
		d.odd, d.hasOdd = b, true
		d.nextOff++
		return Token{Kind: Need}
	}
	off := d.nextOff - 1
	u := d.unit(d.odd, b)
	d.hasOdd = false
	d.nextOff++
	if d.auto {
		d.auto = false
		if u == 0xFEFF || u == 0xFFFE {
			d.order = orderOf(d.odd, b)
		}
		if u == 0xFEFF {
			return Token{R: scalar.BOM, Kind: OK, Offset: off, Len: 2, BOM: true}
		}
	}
	if d.hasHi {
		hiOff := d.hiOff
		d.hasHi = false
		if scalar.IsLowSurrogate(u) {
			return Token{R: scalar.CombineSurrogates(d.hi, u), Kind: OK, Offset: hiOff, Len: 4}
		}
		t := Token{Kind: Bad, Offset: hiOff, Len: 2}
		switch {
		case scalar.IsHighSurrogate(u):
			d.hi, d.hasHi, d.hiOff = u, true, off // re-armed pending high
		case scalar.IsLowSurrogate(u):
			d.extra, d.hasExtra = Token{Kind: Bad, Offset: off, Len: 2}, true
		default:
			d.extra, d.hasExtra = Token{R: rune(u), Kind: OK, Offset: off, Len: 2}, true
		}
		return t
	}
	switch {
	case scalar.IsHighSurrogate(u):
		d.hi, d.hasHi, d.hiOff = u, true, off
		return Token{Kind: Need}
	case scalar.IsLowSurrogate(u):
		return Token{Kind: Bad, Offset: off, Len: 2}
	default:
		return Token{R: rune(u), Kind: OK, Offset: off, Len: 2}
	}
}

func orderOf(a, b byte) int {
	if a == 0xFE && b == 0xFF {
		return BE
	}
	return LE
}

// Poll drains an extra token produced while resolving a bad high surrogate.
func (d *Decoder) Poll() (Token, bool) {
	t, ok := d.extra, d.hasExtra
	d.hasExtra = false
	return t, ok
}

// EOF flushes a lone trailing byte or an unpaired high surrogate.
func (d *Decoder) EOF() Token {
	switch {
	case d.hasHi:
		off := d.hiOff
		d.hasHi = false
		return Token{Kind: Bad, Offset: off, Len: 2}
	case d.hasOdd:
		d.hasOdd = false
		return Token{Kind: Bad, Offset: d.nextOff - 1, Len: 1}
	}
	return Token{Kind: Need}
}

// Encode appends the UTF-16 encoding of r in the given byte order.
func Encode(dst []byte, order int, r rune) []byte {
	put := func(u uint16) {
		if order == BE {
			dst = append(dst, byte(u>>8), byte(u))
		} else {
			dst = append(dst, byte(u), byte(u>>8))
		}
	}
	if r >= 0x10000 {
		r -= 0x10000
		put(0xD800 | uint16(r>>10))
		put(0xDC00 | uint16(r&0x3FF))
	} else {
		put(uint16(r))
	}
	return dst
}
