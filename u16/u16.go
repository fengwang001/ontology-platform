// Package u16 decodes and encodes UTF-16 code units (LE/BE) without
// relying on the unicode/utf16 package.
package u16

import "ontology/scalar"

// Order selects a UTF-16 byte order.
type Order int

const (
	LE Order = iota
	BE
)

type Kind int

const (
	Rune       Kind = iota // decoded scalar (N is 2 or 4)
	Invalid                // one illegal unit
	Incomplete             // odd byte or lone high surrogate at end
	NeedMore
)

// Event is one Feed result. Again holds bytes rejected for reprocessing.
type Event struct {
	Kind  Kind
	Rune  rune
	N     int
	Again []byte
}

// Decoder is a stateful incremental UTF-16 decoder of fixed order.
type Decoder struct {
	o     Order
	odd   byte
	has   bool
	hi    uint16
	hiSet bool
}

func NewDecoder(o Order) *Decoder { return &Decoder{o: o} }

func (d *Decoder) unit(a, b byte) uint16 {
	if d.o == LE {
		return uint16(a) | uint16(b)<<8
	}
	return uint16(a)<<8 | uint16(b)
}

func isHigh(u uint16) bool { return u >= 0xD800 && u <= 0xDBFF }
func isLow(u uint16) bool  { return u >= 0xDC00 && u <= 0xDFFF }

// Feed supplies one byte and returns one event.
func (d *Decoder) Feed(b byte) Event {
	if !d.has {
		d.odd, d.has = b, true
		return Event{Kind: NeedMore}
	}
	a := d.odd
	d.has = false
	u := d.unit(a, b)
	if d.hiSet {
		d.hiSet = false
		if isLow(u) {
			r := 0x10000 + (rune(d.hi)-0xD800)<<10 + (rune(u) - 0xDC00)
			return Event{Kind: Rune, Rune: r, N: 4}
		}
		return Event{Kind: Invalid, N: 2, Again: []byte{a, b}}
	}
	switch {
	case isHigh(u):
		d.hi, d.hiSet = u, true
		return Event{Kind: NeedMore}
	case isLow(u):
		return Event{Kind: Invalid, N: 2}
	default:
		return Event{Kind: Rune, Rune: rune(u), N: 2}
	}
}

// Len returns buffered byte count (0..3: odd byte plus pending high).
func (d *Decoder) Len() int {
	n := 0
	if d.has {
		n++
	}
	if d.hiSet {
		n += 2
	}
	return n
}

func (d *Decoder) Reset() { d.has, d.hiSet = false, false }

// Flush resolves buffered bytes at end of stream.
func (d *Decoder) Flush() Event {
	if d.has { // odd trailing byte
		n := 1
		if d.hiSet {
			n = 3
		}
		d.has, d.hiSet = false, false
		return Event{Kind: Incomplete, N: n}
	}
	if d.hiSet {
		d.hiSet = false
		return Event{Kind: Incomplete, N: 2}
	}
	return Event{Kind: NeedMore}
}

func emit16(dst []byte, u uint16, o Order) []byte {
	if o == LE {
		return append(dst, byte(u), byte(u>>8))
	}
	return append(dst, byte(u>>8), byte(u))
}

// Encode appends the UTF-16 encoding of r in order o to dst.
func Encode(dst []byte, r rune, o Order) []byte {
	if !scalar.Valid(r) {
		return emit16(dst, 0xFFFD, o)
	}
	if r < 0x10000 {
		return emit16(dst, uint16(r), o)
	}
	r -= 0x10000
	dst = emit16(dst, 0xD800+uint16(r>>10), o)
	return emit16(dst, 0xDC00+uint16(r&0x3FF), o)
}
