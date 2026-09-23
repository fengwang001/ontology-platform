// Package u16 decodes and encodes UTF-16LE and UTF-16BE at the byte level.
package u16

import "ontology/scalar"

// Order selects a UTF-16 byte order.
type Order int

const (
	LE Order = iota
	BE
)

// Event is one decoded unit; Consume counts input bytes.
type Event struct {
	R       rune
	OK      bool
	BOM     bool
	Consume int
}

// Decoder is a byte-at-a-time decoder. Not safe for concurrent use.
type Decoder struct {
	order     Order
	detectBOM bool
	off       int
	lo        byte
	haveByte  bool
	high      rune
	haveHigh  bool
	bomDone   bool
}

// NewDecoder builds a decoder; detectBOM only acts at absolute offset 0.
func NewDecoder(order Order, detectBOM bool) *Decoder {
	return &Decoder{order: order, detectBOM: detectBOM}
}

// Push feeds one byte and may return one or two events (a replayed unit).
func (d *Decoder) Push(b byte) []Event {
	if !d.haveByte {
		d.lo, d.haveByte = b, true
		return nil
	}
	d.haveByte = false
	u := d.unit(d.lo, b)
	off := d.off
	d.off += 2
	if d.detectBOM && !d.bomDone && off == 0 {
		if ev, ok := d.bom(d.lo, b); ok {
			d.bomDone = true
			return []Event{ev}
		}
	}
	d.bomDone = true
	return d.unitEvent(u)
}

// Close reports a dangling odd byte or high surrogate.
func (d *Decoder) Close() (Event, bool) {
	switch {
	case d.haveByte:
		d.haveByte = false
		return Event{Consume: 1}, true
	case d.haveHigh:
		d.haveHigh = false
		return Event{Consume: 2}, true
	}
	return Event{}, false
}

func (d *Decoder) unitEvent(u rune) []Event {
	if d.haveHigh {
		high := d.high
		d.haveHigh = false
		if scalar.IsLowSurrogate(u) {
			return []Event{{R: scalar.SurrogatePair(high, u), OK: true, Consume: 2}}
		}
		out := []Event{{Consume: 2}} // stranded high; u is replayed below
		out = append(out, d.unitEvent(u)...)
		return out
	}
	switch {
	case scalar.IsHighSurrogate(u):
		d.high, d.haveHigh = u, true
		return nil
	case scalar.IsLowSurrogate(u):
		return []Event{{Consume: 2}}
	default:
		return []Event{{R: u, OK: true, Consume: 2}}
	}
}

func (d *Decoder) unit(first, second byte) rune {
	if d.order == LE {
		return rune(first) | rune(second)<<8
	}
	return rune(second) | rune(first)<<8
}

func (d *Decoder) bom(first, second byte) (Event, bool) {
	switch {
	case first == 0xFE && second == 0xFF:
		d.order = BE
	case first == 0xFF && second == 0xFE:
		d.order = LE
	default:
		return Event{}, false
	}
	return Event{R: scalar.BOM, OK: true, BOM: true, Consume: 2}, true
}

// Encode appends r in the chosen byte order (a surrogate pair when needed).
func Encode(p []byte, r rune, o Order) []byte {
	if r >= 0x10000 {
		hi, lo := scalar.SplitSurrogate(r)
		p = encodeUnit(p, hi, o)
		return encodeUnit(p, lo, o)
	}
	return encodeUnit(p, r, o)
}

// Len is the encoded byte length of r.
func Len(r rune) int {
	if r >= 0x10000 {
		return 4
	}
	return 2
}

func encodeUnit(p []byte, u rune, o Order) []byte {
	hi, lo := byte(u>>8), byte(u)
	if o == LE {
		return append(p, lo, hi)
	}
	return append(p, hi, lo)
}
