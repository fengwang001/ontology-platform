package u8

import "ontology/scalar"

type Unit struct {
	R       rune
	Size    int
	Legal   bool
	Partial bool
}

type Decoder struct {
	prefix [3]byte
	n      int
	want   int
	lo     rune
}

func NewDecoder() *Decoder { return &Decoder{} }

func (d *Decoder) Pending() []byte { return append([]byte(nil), d.prefix[:d.n]...) }

func (d *Decoder) InUnit() bool { return d.n > 0 }

func (d *Decoder) Feed(b byte) (Unit, bool, bool) {
	if d.n == 0 {
		u := d.start(b)
		if u.Size != 0 {
			return u, true, true
		}
		return Unit{}, false, true
	}
	ok := scalar.Continuation(b)
	if d.n == 1 {
		switch d.prefix[0] {
		case 0xE0:
			ok = ok && b >= 0xA0
		case 0xED:
			ok = ok && b <= 0x9F
		case 0xF0:
			ok = ok && b >= 0x90
		case 0xF4:
			ok = ok && b <= 0x8F
		}
		if !ok {
			size := d.n
			d.reset()
			return Unit{Size: size, Partial: true}, true, false
		}
	} else if !ok {
		size := d.n + 1
		d.reset()
		return Unit{Size: size, Partial: true}, true, true
	}
	d.prefix[d.n] = b
	d.n++
	if d.n < d.want {
		return Unit{}, false, true
	}
	r, size := d.value(), d.n
	d.reset()
	if r < d.lo || !scalar.Valid(r) {
		return Unit{Size: size, Partial: true}, true, true
	}
	return Unit{R: r, Size: size, Legal: true}, true, true
}

func (d *Decoder) Close() Unit {
	if d.n == 0 {
		return Unit{}
	}
	size := d.n
	d.reset()
	return Unit{Size: size, Partial: true}
}

func Encode(r rune) []byte {
	if !scalar.Valid(r) {
		r = scalar.Replacement
	}
	switch {
	case r < 0x80:
		return []byte{byte(r)}
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r&0x3F)}
	case r < 0x10000:
		return []byte{0xE0 | byte(r>>12), 0x80 | byte((r>>6)&0x3F), 0x80 | byte(r&0x3F)}
	default:
		return []byte{0xF0 | byte(r>>18), 0x80 | byte((r>>12)&0x3F), 0x80 | byte((r>>6)&0x3F), 0x80 | byte(r&0x3F)}
	}
}

const Replacement = scalar.Replacement

func (d *Decoder) reset() { d.n, d.want, d.lo = 0, 0, 0 }

func (d *Decoder) start(b byte) Unit {
	d.prefix[0] = b
	switch {
	case b < 0x80:
		return Unit{R: rune(b), Size: 1, Legal: true}
	case b >= 0xC2 && b <= 0xDF:
		d.n, d.want, d.lo = 1, 2, 0x80
	case b >= 0xE0 && b <= 0xEF:
		d.n, d.want, d.lo = 1, 3, 0x800
	case b >= 0xF0 && b <= 0xF4:
		d.n, d.want, d.lo = 1, 4, 0x10000
	default:
		return Unit{Size: 1, Partial: true}
	}
	return Unit{}
}

func (d *Decoder) value() rune {
	b := d.prefix[:d.want]
	switch d.want {
	case 2:
		return rune(b[0]&0x1F)<<6 | rune(b[1]&0x3F)
	case 3:
		return rune(b[0]&0x0F)<<12 | rune(b[1]&0x3F)<<6 | rune(b[2]&0x3F)
	default:
		return rune(b[0]&0x07)<<18 | rune(b[1]&0x3F)<<12 | rune(b[2]&0x3F)<<6 | rune(b[3]&0x3F)
	}
}
