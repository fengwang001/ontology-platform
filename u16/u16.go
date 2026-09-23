package u16

import "ontology/scalar"

type Order int

const (
	Auto Order = iota
	LittleEndian
	BigEndian
)

type EventKind int

const (
	Scalar EventKind = iota
	Invalid
	Truncated
)

type Event struct {
	Kind  EventKind
	Rune  rune
	Bytes int
	BOM   bool
}

type Decoder struct {
	order   Order
	high    uint16
	hasHigh bool
	byte0   byte
	half    bool
	checks  int64
}

func NewDecoder(order Order) *Decoder { return &Decoder{order: order} }

func (d *Decoder) Pending() int {
	n := 0
	if d.hasHigh {
		n += 2
	}
	if d.half {
		n++
	}
	return n
}

func (d *Decoder) Feed(p []byte, eof bool) (Event, int) {
	if d.half {
		d.checks++
		if len(p) == 0 {
			if eof {
				d.half = false
				return Event{Kind: Truncated, Rune: scalar.Replacement, Bytes: 1}, 0
			}
			return Event{}, 0
		}
		p = append([]byte{d.byte0}, p...)
		d.half = false
	}
	if len(p) == 0 {
		if eof && d.hasHigh {
			d.hasHigh = false
			return Event{Kind: Truncated, Rune: scalar.Replacement, Bytes: 2}, 0
		}
		return Event{}, 0
	}
	if len(p) == 1 {
		d.checks++
		if eof {
			return Event{Kind: Truncated, Rune: scalar.Replacement, Bytes: 1}, 1
		}
		d.byte0, d.half = p[0], true
		return Event{}, 1
	}
	d.checks += 2
	u, n := d.unit(p[0], p[1]), 2
	if d.hasHigh {
		d.hasHigh = false
		if scalar.IsLowSurrogate(u) {
			r, _ := scalar.DecodeSurrogate(d.high, u)
			return Event{Kind: Scalar, Rune: r, Bytes: 4}, n
		}
		return Event{Kind: Invalid, Rune: scalar.Replacement, Bytes: 2}, 0
	}
	if u == 0xFEFF || d.order == Auto && u == 0xFFFE {
		if d.order == Auto {
			d.order = BigEndian
			if u == 0xFFFE {
				d.order = LittleEndian
			}
		}
		return Event{Kind: Scalar, Rune: 0xFEFF, Bytes: 2, BOM: true}, n
	}
	switch {
	case scalar.IsHighSurrogate(u):
		d.high, d.hasHigh = u, true
		if eof && len(p) == 2 {
			d.hasHigh = false
			return Event{Kind: Truncated, Rune: scalar.Replacement, Bytes: 2}, n
		}
		return Event{}, n
	case scalar.IsLowSurrogate(u):
		return Event{Kind: Invalid, Rune: scalar.Replacement, Bytes: 2}, n
	default:
		return Event{Kind: Scalar, Rune: rune(u), Bytes: 2}, n
	}
}

func (d *Decoder) unit(a, b byte) uint16 {
	if d.order == BigEndian {
		return uint16(a)<<8 | uint16(b)
	}
	return uint16(b)<<8 | uint16(a)
}

func Encode(r rune, order Order) []byte {
	put := func(u uint16) []byte {
		a, b := byte(u>>8), byte(u)
		if order == LittleEndian {
			a, b = b, a
		}
		return []byte{a, b}
	}
	if high, low, ok := scalar.EncodeSurrogate(r); ok {
		return append(put(high), put(low)...)
	}
	return put(uint16(r))
}
