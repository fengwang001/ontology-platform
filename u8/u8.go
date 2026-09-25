package u8

import "ontology/scalar"

const (
	Scalar = iota
	BOM
	Invalid
	Truncated
)

type Event struct {
	Kind  int
	R     scalar.Value
	Start int
	Len   int
}

type Decoder struct {
	buf     [3]byte
	n       int
	need    int
	start   int
	total   int
	checked uint64
}

func (d *Decoder) Checks() uint64 { return d.checked }

func Cont(b byte) bool { return b&0xC0 == 0x80 }

func (d *Decoder) Feed(b byte) Event {
	d.checked++
	start := d.total
	d.total++
	if d.n == 0 {
		switch {
		case b < 0x80:
			return Event{Scalar, scalar.Value(b), start, 1}
		case b >= 0xC2 && b <= 0xDF:
			d.set(b, 2, start)
		case b >= 0xE0 && b <= 0xEF:
			d.set(b, 3, start)
		case b >= 0xF0 && b <= 0xF4:
			d.set(b, 4, start)
		default:
			return Event{Invalid, scalar.Replacement, start, 1}
		}
		return Event{}
	}

	lead := d.buf[0]
	position := d.n
	d.checked++
	if !Cont(b) || position == 1 && !secondOK(lead, b) {
		n := d.n + 1
		d.n = 0
		return Event{Invalid, scalar.Replacement, d.start, n}
	}
	d.buf[d.n] = b
	d.n++
	if d.n != d.need {
		return Event{}
	}

	r := decode(lead, d.buf[1:d.n])
	length := d.need
	start := d.start
	d.n = 0
	if lead == 0xEF && r == 0xFEFF {
		return Event{BOM, r, start, length}
	}
	return Event{Scalar, r, start, length}
}

func (d *Decoder) set(lead byte, need int, start int) {
	d.buf[0], d.n, d.need, d.start = lead, 1, need, start
}

func (d *Decoder) Close() Event {
	if d.n == 0 {
		return Event{}
	}
	n, start := d.n, d.start
	d.n = 0
	return Event{Truncated, scalar.Replacement, start, n}
}

func secondOK(lead, b byte) bool {
	switch lead {
	case 0xE0:
		return b >= 0xA0
	case 0xED:
		return b <= 0x9F
	case 0xF0:
		return b >= 0x90
	case 0xF4:
		return b <= 0x8F
	default:
		return true
	}
}

func decode(lead byte, rest []byte) scalar.Value {
	switch len(rest) + 1 {
	case 2:
		return scalar.Value(lead&0x1F)<<6 | scalar.Value(rest[0]&0x3F)
	case 3:
		return scalar.Value(lead&0x0F)<<12 |
			scalar.Value(rest[0]&0x3F)<<6 | scalar.Value(rest[1]&0x3F)
	default:
		return scalar.Value(lead&0x07)<<18 |
			scalar.Value(rest[0]&0x3F)<<12 |
			scalar.Value(rest[1]&0x3F)<<6 | scalar.Value(rest[2]&0x3F)
	}
}

func Encode(out []byte, r scalar.Value) []byte {
	switch {
	case r <= 0x7F:
		return append(out, byte(r))
	case r <= 0x7FF:
		return append(out, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
	case r <= 0xFFFF:
		return append(out, 0xE0|byte(r>>12), 0x80|byte(r>>6&0x3F), 0x80|byte(r&0x3F))
	default:
		return append(out, 0xF0|byte(r>>18), 0x80|byte(r>>12&0x3F),
			0x80|byte(r>>6&0x3F), 0x80|byte(r&0x3F))
	}
}

func BOM(out []byte) []byte { return append(out, 0xEF, 0xBB, 0xBF) }
