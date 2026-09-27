package u16

import "ontology/scalar"

type Unit struct {
	R       rune
	Size    int
	Invalid bool
}

type Decoder struct {
	Little bool
	pend   byte
	haveP  bool
	high   uint16
	haveH  bool
	queued uint16
	haveQ  bool
}

func (d *Decoder) Step(p []byte) (Unit, int) {
	if d.haveQ {
		u := d.queued
		d.queued, d.haveQ = 0, false
		return d.fromUnit(u), 0
	}
	if d.haveP {
		if len(p) == 0 {
			size := 1
			if d.haveH {
				size = 3
			}
			d.haveP = false
			d.high, d.haveH = 0, false
			return Unit{Size: size, Invalid: true}, 0
		}
		u := pair(d.pend, p[0], d.Little)
		d.pend, d.haveP = 0, false
		out, _ := d.unit(u)
		return out, 2
	}
	if d.haveH {
		if len(p) < 2 {
			if len(p) == 1 {
				d.pend, d.haveP = p[0], true
			}
			if len(p) == 0 {
				d.high, d.haveH = 0, false
				return Unit{Size: 2, Invalid: true}, 0
			}
			return Unit{}, len(p)
		}
		u := pair(p[0], p[1], d.Little)
		if scalar.LowSurrogate(rune(u)) {
			r := 0x10000 + (rune(d.high)-0xD800)<<10 + (rune(u)-0xDC00)
			d.high, d.haveH = 0, false
			return Unit{R: r, Size: 4}, 2
		}
		d.queued, d.haveQ = u, true
		d.high, d.haveH = 0, false
		return Unit{R: scalar.Replacement, Size: 2, Invalid: true}, 0
	}
	if len(p) == 0 {
		return Unit{}, 0
	}
	if len(p) == 1 {
		d.pend, d.haveP = p[0], true
		return Unit{}, 1
	}
	return d.unit(pair(p[0], p[1], d.Little))
}

func (d *Decoder) unit(u uint16) (Unit, int) {
	r := rune(u)
	if scalar.HighSurrogate(r) {
		d.high, d.haveH = u, true
		return Unit{}, 2
	}
	return d.fromUnit(u), 2
}

func (d *Decoder) fromUnit(u uint16) Unit {
	r := rune(u)
	if r >= 0xDC00 && r <= 0xDFFF || r >= 0xD800 && r <= 0xDBFF {
		return Unit{R: scalar.Replacement, Size: 2, Invalid: true}
	}
	return Unit{R: r, Size: 2}
}

func (d *Decoder) Close() Unit {
	u, _ := d.Step(nil)
	return u
}

func pair(a, b byte, little bool) uint16 {
	if little {
		return uint16(b)<<8 | uint16(a)
	}
	return uint16(a)<<8 | uint16(b)
}

func Encode(r rune, little bool) []byte {
	put := func(u uint16) []byte {
		if little {
			return []byte{byte(u), byte(u >> 8)}
		}
		return []byte{byte(u >> 8), byte(u)}
	}
	if r >= 0x10000 {
		r -= 0x10000
		hi := uint16(0xD800 + r>>10)
		lo := uint16(0xDC00 + r&0x3FF)
		return append(put(hi), put(lo)...)
	}
	return put(uint16(r))
}
