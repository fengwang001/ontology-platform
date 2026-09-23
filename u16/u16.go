package u16

import "ontology/scalar"

type Endian uint8

const (
	Little Endian = iota + 1
	Big
)

type Kind uint8

const (
	Invalid Kind = iota + 1
	Scalar
)

type Event struct {
	Kind Kind
	R    uint32
	Len  int
}

type Decoder struct {
	order   Endian
	pending byte
	have    bool
	high    uint16
	hasHigh bool
	Checked int
}

func NewDecoder(order Endian) *Decoder { return &Decoder{order: order} }

func (d *Decoder) SetOrder(order Endian) { d.order = order }

func highSurrogate(u uint16) bool { return u >= 0xD800 && u <= 0xDBFF }

func lowSurrogate(u uint16) bool { return u >= 0xDC00 && u <= 0xDFFF }

func (d *Decoder) unit(u uint16) []Event {
	if d.hasHigh {
		if lowSurrogate(u) {
			r := 0x10000 + (uint32(d.high-0xD800) << 10) + uint32(u-0xDC00)
			d.hasHigh = false
			return []Event{{Kind: Scalar, R: r, Len: 4}}
		}
		d.hasHigh = false
		if highSurrogate(u) {
			d.high = u
			d.hasHigh = true
			return []Event{{Kind: Invalid, Len: 2}}
		}
		if scalar.Surrogate(uint32(u)) {
			return []Event{{Kind: Invalid, Len: 2}, {Kind: Invalid, Len: 2}}
		}
		return []Event{{Kind: Invalid, Len: 2}, {Kind: Scalar, R: uint32(u), Len: 2}}
	}
	if highSurrogate(u) {
		d.high, d.hasHigh = u, true
		return nil
	}
	if scalar.Surrogate(uint32(u)) {
		return []Event{{Kind: Invalid, Len: 2}}
	}
	return []Event{{Kind: Scalar, R: uint32(u), Len: 2}}
}

func (d *Decoder) Step(b byte) (events []Event, replay byte, replayed bool) {
	d.Checked++
	if !d.have {
		d.pending, d.have = b, true
		return nil, 0, false
	}
	var u uint16
	if d.order == Little {
		u = uint16(d.pending) | uint16(b)<<8
	} else {
		u = uint16(d.pending)<<8 | uint16(b)
	}
	d.have = false
	return d.unit(u), 0, false
}

func (d *Decoder) Close() (Event, bool) {
	if d.hasHigh {
		d.hasHigh = false
		return Event{Kind: Invalid, Len: 2}, true
	}
	if d.have {
		d.have = false
		return Event{Kind: Invalid, Len: 1}, true
	}
	return Event{}, false
}

func (d *Decoder) PendingLen() int {
	n := 0
	if d.have {
		n++
	}
	if d.hasHigh {
		n += 2
	}
	return n
}

func appendUnit(out []byte, u uint16, order Endian) []byte {
	if order == Little {
		return append(out, byte(u), byte(u>>8))
	}
	return append(out, byte(u>>8), byte(u))
}

func AppendEncode(out []byte, r uint32, order Endian) ([]byte, bool) {
	if !scalar.Valid(r) || order == 0 {
		return out, false
	}
	if r < 0x10000 {
		return appendUnit(out, uint16(r), order), true
	}
	r -= 0x10000
	out = appendUnit(out, 0xD800+uint16(r>>10), order)
	return appendUnit(out, 0xDC00+uint16(r&0x3FF), order), true
}

func EncodedLen(r uint32) int {
	if !scalar.Valid(r) {
		return 0
	}
	if r < 0x10000 {
		return 2
	}
	return 4
}
