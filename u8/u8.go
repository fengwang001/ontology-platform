package u8

import "ontology/scalar"

type Unit struct {
	R       rune
	Size    int
	Invalid bool
}

type Decoder struct {
	code rune
	need int
	total int
	lo   byte
	hi   byte
}

func (d *Decoder) Step(p []byte) (Unit, int) {
	if d.need == 0 {
		if len(p) == 0 {
			return Unit{}, 0
		}
		b := p[0]
		switch {
		case b < 0x80:
			return Unit{R: rune(b), Size: 1}, 1
		case b == 0xC0 || b == 0xC1 || b > 0xF4:
			return Unit{Size: 1, Invalid: true}, 1
		case b < 0xC2:
			return Unit{Size: 1, Invalid: true}, 1
		case b < 0xE0:
			d.code, d.need, d.total, d.lo, d.hi = rune(b&0x1F), 1, 2, 0x80, 0xBF
		case b < 0xF0:
			d.code = rune(b & 0x0F)
			d.need, d.total = 2, 3
			d.lo, d.hi = secondRange3(b)
		default:
			d.code = rune(b & 0x07)
			d.need, d.total = 3, 4
			d.lo, d.hi = secondRange4(b)
		}
	}
	if len(p) == 0 {
		size := d.total
		d.Reset()
		return Unit{Size: size, Invalid: true}, 0
	}
	b := p[0]
	if b < d.lo || b > d.hi {
		size := d.total - d.need + 1
		if d.need == d.total-1 {
			size = 1
		}
		d.Reset()
		consumed := 1
		if d.need == d.total-1 {
			consumed = 0
		}
		return Unit{Size: size, Invalid: true}, consumed
	}
	d.code = d.code<<6 | rune(b&0x3F)
	d.need--
	d.lo, d.hi = 0x80, 0xBF
	if d.need > 0 {
		return Unit{}, 1
	}
	r := d.code
	d.Reset()
	if !scalar.Valid(r) {
		return Unit{Size: len(Encode(r)), Invalid: true}, 1
	}
	return Unit{R: r, Size: len(Encode(r))}, 1
}

func (d *Decoder) Reset() { *d = Decoder{} }

func secondRange3(b byte) (byte, byte) {
	switch b {
	case 0xE0:
		return 0xA0, 0xBF
	case 0xED:
		return 0x80, 0x9F
	default:
		return 0x80, 0xBF
	}
}

func secondRange4(b byte) (byte, byte) {
	switch b {
	case 0xF4:
		return 0x80, 0x8F
	case 0xF0:
		return 0x90, 0xBF
	default:
		return 0x80, 0xBF
	}
}

func Encode(r rune) []byte {
	switch {
	case r < 0 || r > scalar.MaxRune || scalar.Surrogate(r):
		return []byte{0xEF, 0xBF, 0xBD}
	case r < 0x80:
		return []byte{byte(r)}
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r&0x3F)}
	case r < 0x10000:
		return []byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6&0x3F), 0x80 | byte(r&0x3F)}
	default:
		return []byte{0xF0 | byte(r>>18), 0x80 | byte(r>>12&0x3F), 0x80 | byte(r>>6&0x3F), 0x80 | byte(r&0x3F)}
	}
	return []byte{0xEF, 0xBF, 0xBD}
}
