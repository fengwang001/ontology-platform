// Package u16 decodes and encodes UTF-16 (LE/BE) at byte level.
package u16

import "ontology/scalar"

// Endian selects byte order.
const (
	LE = 0
	BE = 1
)

// Decoder event kinds reuse the same numeric meaning as u8:
// Need / Scalar / Illegal / Retry, where Retry re-feeds b.
const (
	Need    = 0
	Scalar  = 1
	Illegal = 2
	Retry   = 3
)

// Event is one Feed result.
type Event struct {
	Kind int
	Rune rune
	Len  int
}

// Decoder consumes one byte at a time.
type Decoder struct {
	endian int
	half   int // 0 none, 1 one byte buffered
	hi     int // >0 a high surrogate unit is held
	b0     byte
	ub     uint16
	Checks int64
}

// NewDecoder sets byte order.
func NewDecoder(endian int) *Decoder { return &Decoder{endian: endian} }

func unit(b0, b1 byte, endian int) uint16 {
	if endian == LE {
		return uint16(b0) | uint16(b1)<<8
	}
	return uint16(b0)<<8 | uint16(b1)
}

// Feed examines one byte. A lone high surrogate followed by a non-low
// surrogate yields Illegal Len=2 then Retry, so the next unit's first byte is
// re-parsed as a new character (never swallowed).
func (d *Decoder) Feed(b byte) Event {
	d.Checks++
	if d.hi == 2 {
		if d.half == 0 {
			d.b0, d.half = b, 1
			return Event{Kind: Need}
		}
		d.half = 0
		u := unit(d.b0, b, d.endian)
		d.hi = 0
		if scalar.LowSurrogate(u) {
			return Event{Kind: Scalar, Rune: scalar.CombineSurrogates(d.ub, u), Len: 2}
		}
		return Event{Kind: Retry, Len: 0} // high surrogate illegal; re-feed b
	}
	if d.half == 0 {
		d.b0, d.half = b, 1
		return Event{Kind: Need}
	}
	d.half = 0
	u := unit(d.b0, b, d.endian)
	switch {
	case scalar.HighSurrogate(u):
		d.ub, d.hi = u, 2
		return Event{Kind: Need}
	case scalar.LowSurrogate(u):
		return Event{Kind: Illegal, Len: 2}
	default:
		return Event{Kind: Scalar, Rune: rune(u), Len: 2}
	}
}

// Pending reports buffered valid bytes: 0,1 (half unit) or 3 (hi+half).
func (d *Decoder) Pending() int {
	if d.hi == 2 {
		return 2 + d.half
	}
	return d.half
}

// Drop discards the buffered prefix; returns its length.
func (d *Decoder) Drop() int {
	n := d.Pending()
	d.half, d.hi = 0, 0
	return n
}

// Encode appends the UTF-16 units of r in the chosen byte order.
func Encode(dst []byte, r rune, endian int) []byte {
	put := func(u uint16) {
		if endian == LE {
			dst = append(dst, byte(u), byte(u>>8))
		} else {
			dst = append(dst, byte(u>>8), byte(u))
		}
}
	if r >= 0x10000 {
		v := uint32(r) - 0x10000
		put(uint16(0xD800 + v>>10))
		put(uint16(0xDC00 + v&0x3FF))
	} else {
		put(uint16(r))
	}
	return dst
}
