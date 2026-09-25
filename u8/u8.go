package u8

import "ontology/scalar"

type Decoder struct {
	buf  [4]byte
	n    int
	need int
}

func NewDecoder() *Decoder { return &Decoder{} }

func (d *Decoder) Pending() int { return d.n }

func (d *Decoder) Step(b byte) (rune, int, bool) {
	if d.n == 0 {
		switch {
		case b < 0x80:
			return rune(b), 1, true
		case b >= 0xC2 && b <= 0xDF:
			d.buf[0], d.n, d.need = b, 1, 2
		case b >= 0xE0 && b <= 0xEF:
			d.buf[0], d.n, d.need = b, 1, 3
		case b >= 0xF0 && b <= 0xF4:
			d.buf[0], d.n, d.need = b, 1, 4
		default:
			return scalar.Replacement, 1, false
		}
		return 0, 0, false
	}
	if !continuation(b) || !d.allowedNext(b) {
		size := d.n
		d.n, d.need = 0, 0
		return scalar.Replacement, size, false
	}
	d.buf[d.n] = b
	d.n++
	if d.n < d.need {
		return 0, 0, false
	}
	r := d.decode()
	size := d.need
	d.n, d.need = 0, 0
	if scalar.IsScalar(r) {
		return r, size, true
	}
	return scalar.Replacement, size, false
}

func (d *Decoder) End() (rune, int, bool) {
	if d.n == 0 {
		return 0, 0, false
	}
	size := d.n
	d.n, d.need = 0, 0
	return scalar.Replacement, size, false
}

func Append(out []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(out, byte(r))
	case r < 0x800:
		return append(out, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
	case r < 0x10000:
		return append(out, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	default:
		return append(out, 0xF0|byte(r>>18), 0x80|byte(r>>12)&0x3F,
			0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	}
}

func CrossingBoundary(data []byte, at, after int) int {
	start := after - 3
	if start < at {
		start = at
	}
	for p := start; p < after; p++ {
		_, end := firstEnd(data[p:], at, p)
		if end > after {
			if p > at {
				return p
			}
			return end
		}
	}
	return after
}

func continuation(b byte) bool { return b >= 0x80 && b <= 0xBF }

func (d *Decoder) allowedNext(b byte) bool {
	switch {
	case d.n == 1 && d.buf[0] == 0xE0:
		return b >= 0xA0
	case d.n == 1 && d.buf[0] == 0xED:
		return b <= 0x9F
	case d.n == 1 && d.buf[0] == 0xF0:
		return b >= 0x90
	case d.n == 1 && d.buf[0] == 0xF4:
		return b <= 0x8F
	default:
		return continuation(b)
	}
}

func (d *Decoder) decode() rune {
	switch d.need {
	case 2:
		return rune(d.buf[0]&0x1F)<<6 | rune(d.buf[1]&0x3F)
	case 3:
		return rune(d.buf[0]&0x0F)<<12 | rune(d.buf[1]&0x3F)<<6 | rune(d.buf[2]&0x3F)
	default:
		return rune(d.buf[0]&0x07)<<18 | rune(d.buf[1]&0x3F)<<12 |
			rune(d.buf[2]&0x3F)<<6 | rune(d.buf[3]&0x3F)
	}
}

func firstEnd(data []byte, lower, pos int) (int, int) {
	if len(data) == 0 {
		return pos, pos
	}
	b := data[0]
	switch {
	case b < 0x80:
		return pos, pos + 1
	case b >= 0xC2 && b <= 0xDF:
		return scanLead(data, pos, 2, func(i int) bool { return continuation(data[i]) })
	case b == 0xE0:
		return scanLead(data, pos, 3, func(i int) bool { return i == 1 && data[i] >= 0xA0 || i > 1 && continuation(data[i]) })
	case b >= 0xE1 && b <= 0xEC || b >= 0xEE && b <= 0xEF:
		return scanLead(data, pos, 3, func(i int) bool { return continuation(data[i]) })
	case b == 0xED:
		return scanLead(data, pos, 3, func(i int) bool { return i == 1 && data[i] <= 0x9F || i > 1 && continuation(data[i]) })
	case b == 0xF0:
		return scanLead(data, pos, 4, func(i int) bool { return i == 1 && data[i] >= 0x90 || i > 1 && continuation(data[i]) })
	case b >= 0xF1 && b <= 0xF3:
		return scanLead(data, pos, 4, func(i int) bool { return continuation(data[i]) })
	case b == 0xF4:
		return scanLead(data, pos, 4, func(i int) bool { return i == 1 && data[i] <= 0x8F || i > 1 && continuation(data[i]) })
	default:
		return pos, pos + 1
	}
}

func scanLead(data []byte, pos, need int, ok func(int) bool) (int, int) {
	for i := 1; i < need; i++ {
		if i >= len(data) {
			return pos, len(data)
		}
		if !ok(i) {
			return pos, pos + i
		}
	}
	return pos, pos + need
}
