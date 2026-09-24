package u8

import "ontology/scalar"

type Kind uint8

const (
	OK Kind = iota
	Invalid
	Truncated
)

const MaxCache = 3

type Unit struct {
	Kind  Kind
	R     scalar.Rune
	Start int64
	Size  int
}

type Decoder struct {
	pending  [MaxCache + 1]byte
	have     int
	need     int
	consumed int64
	checks   int64
}

func (d *Decoder) Feed(p []byte) []Unit {
	var units []Unit
	for _, b := range p {
		d.checks++
		d.consumed++
		if d.have == 0 {
			units = d.start(units, b)
		} else {
			units = d.next(units, b)
		}
	}
	return units
}

func (d *Decoder) Flush() (Unit, bool) {
	if d.have == 0 {
		return Unit{}, false
	}
	u := Unit{Kind: Truncated, Start: d.consumed - int64(d.have), Size: d.have}
	d.have, d.need = 0, 0
	return u, true
}

func (d *Decoder) Checks() int64 { return d.checks }
func (d *Decoder) Pending() int  { return d.have }

func (d *Decoder) start(units []Unit, b byte) []Unit {
	switch {
	case b < 0x80:
		return append(units, Unit{OK, scalar.Rune(b), d.consumed - 1, 1})
	case b&0xC0 == 0x80, b < 0xC2, b > 0xF4:
		return append(units, Unit{Invalid, 0, d.consumed - 1, 1})
	case b < 0xE0:
		d.need = 2
	case b < 0xF0:
		d.need = 3
	default:
		d.need = 4
	}
	d.pending[0], d.have = b, 1
	return units
}

func (d *Decoder) next(units []Unit, b byte) []Unit {
	first := d.pending[0]
	if d.have == 1 && (!cont(b) || !secondOK(first, b)) {
		units = append(units, Unit{Invalid, 0, d.consumed - 2, 1})
		d.have, d.need = 0, 0
		return d.start(units, b)
	}
	if d.have > 1 && !cont(b) {
		units = append(units, Unit{Invalid, 0, d.consumed - int64(d.have), d.have})
		d.have, d.need = 0, 0
		return d.start(units, b)
	}
	d.pending[d.have] = b
	d.have++
	if d.have != d.need {
		return units
	}
	r := decodePending(d.pending[:d.have])
	size := d.have
	d.have, d.need = 0, 0
	return append(units, Unit{OK, r, d.consumed - int64(size), size})
}

func cont(b byte) bool { return b&0xC0 == 0x80 }

func secondOK(first, second byte) bool {
	switch first {
	case 0xE0:
		return second >= 0xA0
	case 0xED:
		return second <= 0x9F
	case 0xF0:
		return second >= 0x90
	case 0xF4:
		return second <= 0x8F
	default:
		return true
	}
}

func decodePending(p []byte) scalar.Rune {
	switch len(p) {
	case 2:
		return scalar.Rune(p[0]&0x1F)<<6 | scalar.Rune(p[1]&0x3F)
	case 3:
		return scalar.Rune(p[0]&0x0F)<<12 | scalar.Rune(p[1]&0x3F)<<6 | scalar.Rune(p[2]&0x3F)
	default:
		return scalar.Rune(p[0]&0x07)<<18 | scalar.Rune(p[1]&0x3F)<<12 |
			scalar.Rune(p[2]&0x3F)<<6 | scalar.Rune(p[3]&0x3F)
	}
}

func Encode(p []byte, r scalar.Rune) []byte {
	switch {
	case r <= 0x7F:
		return append(p, byte(r))
	case r <= 0x7FF:
		return append(p, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
	case r <= 0xFFFF:
		return append(p, 0xE0|byte(r>>12), 0x80|byte((r>>6)&0x3F), 0x80|byte(r&0x3F))
	default:
		return append(p, 0xF0|byte(r>>18), 0x80|byte((r>>12)&0x3F),
			0x80|byte((r>>6)&0x3F), 0x80|byte(r&0x3F))
	}
}
