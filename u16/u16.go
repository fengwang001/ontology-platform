// Package u16 decodes and encodes UTF-16LE/UTF-16BE one scalar (or one
// illegal unit) at a time, carrying odd bytes and lone high surrogates.
package u16

import "ontology/scalar"

// Order selects a UTF-16 byte order.
type Order int

const (
	LE Order = iota
	BE
	Auto // BOM selects order; without a BOM, BE is assumed
)

// MaxPending bounds buffered input: one alignment byte plus one held-back
// code unit (a high surrogate's non-low follower).
const MaxPending = 3

// Unit is one decoded outcome; BOM marks a leading U+FEFF consumed as BOM.
type Unit struct {
	R     scalar.Rune
	Len   int
	Bad   bool
	Trunc bool
	BOM   bool
}

// Decoder is a stateful UTF-16 reader; not safe for concurrent use.
type Decoder struct {
	order                        Order
	resolved                     Order
	bomDone                      bool
	haveByte                     bool
	pendByte                     byte
	haveHi                       bool
	hi                           uint16
	haveHeld                     bool
	held                         uint16
	Checks                       int64
}

// NewDecoder builds a decoder in the given order mode.
func NewDecoder(o Order) *Decoder { return &Decoder{order: o, resolved: o} }

// PendingLen reports bytes buffered that are not yet part of a resolved unit.
func (d *Decoder) PendingLen() int {
	n := 0
	if d.haveByte {
		n++
	}
	if d.haveHi {
		n += 2
	}
	if d.haveHeld {
		n += 2
	}
	return n
}

// PendingBytes reconstructs buffered input bytes in their input order.
func (d *Decoder) PendingBytes() []byte {
	if d.haveByte {
		return []byte{d.pendByte}
	}
	if d.haveHi {
		return d.encode(d.hi)
	}
	if d.haveHeld {
		return d.encode(d.held)
	}
	return nil
}

func (d *Decoder) encode(c uint16) []byte {
	if d.resolved == LE {
		return []byte{byte(c), byte(c >> 8)}
	}
	return []byte{byte(c >> 8), byte(c)}
}

// Feed consumes from p, returning the first resolved unit and bytes taken.
func (d *Decoder) Feed(p []byte) (Unit, int) {
	used := 0
	if d.haveHeld {
		c := d.held
		d.haveHeld = false
		return d.dispatch(c), used
	}
	if d.order == Auto && !d.bomDone {
		for used < len(p) && used < 2 {
			d.pendByte = p[used]
			d.Checks++
			used++
			d.haveByte = !d.haveByte
		}
		if used < 2 {
			return Unit{}, used
		}
		c := d.join(p[0], p[1])
		d.bomDone = true
		d.haveByte = false
		switch c {
		case 0xFEFF:
			d.resolved = BE
			return Unit{R: scalar.BOM, Len: 2, BOM: true}, used
		case 0xFFFE:
			d.resolved = LE
			return Unit{R: scalar.BOM, Len: 2, BOM: true}, used
		default:
			d.resolved = BE
			d.held, d.haveHeld = c, true
			return Unit{}, used
		}
	}
	if d.haveByte {
		if len(p) == 0 {
			return Unit{}, 0
		}
		lo := p[0]
		d.Checks++
		used = 1
		c := d.join(d.pendByte, lo)
		d.haveByte = false
		return d.dispatch(c), used
	}
	for used+1 < len(p) {
		d.Checks += 2
		c := d.join(p[used], p[used+1])
		used += 2
		u := d.dispatch(c)
		if u.Len != 0 {
			return u, used
		}
	}
	if used < len(p) {
		d.pendByte = p[used]
		d.haveByte = true
		d.Checks++
		used++
	}
	return Unit{}, used
}

func (d *Decoder) join(hi, lo byte) uint16 {
	if d.resolved == LE {
		return uint16(lo)<<8 | uint16(hi)
	}
	return uint16(hi)<<8 | uint16(lo)
}

func (d *Decoder) dispatch(c uint16) Unit {
	if d.haveHi {
		d.haveHi = false
		if scalar.LoSurrogate(c) {
			return Unit{R: scalar.SurrogatePair(d.hi, c), Len: 4}
		}
		d.held, d.haveHeld = c, true
		return Unit{Len: 2, Bad: true}
	}
	switch {
	case scalar.HiSurrogate(c):
		d.hi, d.haveHi = c, true
		return Unit{}
	case scalar.LoSurrogate(c):
		return Unit{Len: 2, Bad: true}
	default:
		return Unit{R: scalar.Rune(c), Len: 2}
	}
}

// Flush resolves end-of-input residue as a truncated illegal unit.
func (d *Decoder) Flush() Unit {
	switch {
	case d.haveByte:
		d.haveByte = false
		return Unit{Len: 1, Bad: true, Trunc: true}
	case d.haveHi:
		d.haveHi = false
		return Unit{Len: 2, Bad: true, Trunc: true}
	default:
		return Unit{}
	}
}

// Encode appends the UTF-16 encoding of r to dst in order o.
func Encode(dst []byte, r scalar.Rune, o Order) []byte {
	put := func(c uint16) []byte {
		if o == LE {
			return append(dst, byte(c), byte(c>>8))
		}
		return append(dst, byte(c>>8), byte(c))
	}
	if r >= 0x10000 {
		hi, lo := scalar.SplitPair(r)
		dst = put(hi)
		dst = put(lo)
	} else {
		dst = put(uint16(r))
	}
	return dst
}
