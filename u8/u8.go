package u8

import "ontology/scalar"

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
	pend    [3]byte
	n       int
	need    int
	r       uint32
	Checked int
}

func NewDecoder() *Decoder { return &Decoder{} }

func continuation(b byte) bool { return b&0xC0 == 0x80 }

func (d *Decoder) emit() Event {
	e := Event{Kind: Scalar, R: d.r, Len: d.need}
	d.n, d.need, d.r = 0, 0, 0
	return e
}

func invalid(n int) Event { return Event{Kind: Invalid, Len: n} }

func (d *Decoder) lead(b byte) (e Event, start bool) {
	switch {
	case b < 0x80:
		return Event{Kind: Scalar, R: uint32(b), Len: 1}, false
	case b >= 0xC2 && b <= 0xDF:
		d.need, d.n, d.r = 2, 1, uint32(b&0x1F)
	case b >= 0xE0 && b <= 0xEF:
		d.need, d.n, d.r = 3, 1, uint32(b&0x0F)
	case b >= 0xF0 && b <= 0xF4:
		d.need, d.n, d.r = 4, 1, uint32(b&0x07)
	default:
		return invalid(1), false
	}
	d.pend[0] = b
	return Event{}, true
}

func (d *Decoder) Step(b byte) (events []Event, replay byte, replayed bool) {
	d.Checked++
	if d.n == 0 {
		e, _ := d.lead(b)
		if e.Len != 0 {
			return []Event{e}, 0, false
		}
		return nil, 0, false
	}
	second := d.n == 1
	if second {
		switch d.pend[0] {
		case 0xE0:
			if b < 0xA0 || b > 0xBF {
				d.n, d.need = 0, 0
				return []Event{invalid(1)}, b, true
			}
		case 0xED:
			if b < 0x80 || b > 0x9F {
				d.n, d.need = 0, 0
				return []Event{invalid(1)}, b, true
			}
		case 0xF0:
			if b < 0x90 || b > 0xBF {
				d.n, d.need = 0, 0
				return []Event{invalid(1)}, b, true
			}
		case 0xF4:
			if b < 0x80 || b > 0x8F {
				d.n, d.need = 0, 0
				return []Event{invalid(1)}, b, true
			}
		default:
			if !continuation(b) {
				d.n, d.need = 0, 0
				return []Event{invalid(1)}, b, true
			}
		}
	} else if !continuation(b) {
		e := invalid(d.n)
		d.n, d.need, d.r = 0, 0, 0
		return []Event{e}, b, true
	}
	d.pend[d.n-1], d.n = b, d.n+1
	d.r = d.r<<6 | uint32(b&0x3F)
	if d.n == d.need {
		return []Event{d.emit()}, 0, false
	}
	return nil, 0, false
}

func (d *Decoder) Close() (Event, bool) {
	if d.n == 0 {
		return Event{}, false
	}
	e := invalid(d.n)
	d.n, d.need, d.r = 0, 0, 0
	return e, true
}

func (d *Decoder) PendingLen() int { return d.n }

func AppendEncode(out []byte, r uint32) ([]byte, bool) {
	switch {
	case !scalar.Valid(r):
		return out, false
	case r < 0x80:
		return append(out, byte(r)), true
	case r < 0x800:
		return append(out, 0xC0|byte(r>>6), 0x80|byte(r&0x3F)), true
	case r < 0x10000:
		return append(out, 0xE0|byte(r>>12), 0x80|byte(r>>6&0x3F), 0x80|byte(r&0x3F)), true
	default:
		return append(out, 0xF0|byte(r>>18), 0x80|byte(r>>12&0x3F),
			0x80|byte(r>>6&0x3F), 0x80|byte(r&0x3F)), true
	}
}

func EncodedLen(r uint32) int {
	switch {
	case !scalar.Valid(r):
		return 0
	case r < 0x80:
		return 1
	case r < 0x800:
		return 2
	case r < 0x10000:
		return 3
	default:
		return 4
	}
}
