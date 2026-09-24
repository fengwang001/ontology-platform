package u8

import "ontology/scalar"

type Unit struct {
	R       rune
	Size    int
	Bad     bool
	Complete bool
}

type Decoder struct {
	buf [4]byte
	n   int
}

func (d *Decoder) Write(b []byte) (Unit, int) {
	if d.n == 0 && (len(b) == 0 || !start(b[0])) {
		return Unit{Bad: true, Size: 1, Complete: true}, min(1, len(b))
	}
	if d.n == 0 {
		d.buf[0] = b[0]
		d.n = 1
		b = b[1:]
	}
	for d.n < need(d.buf[0]) {
		if len(b) == 0 {
			return Unit{}, 0
		}
		c := b[0]
		if !cont(c) || d.n == 1 && !secondOK(d.buf[0], c) {
			u := Unit{Bad: true, Size: d.n, Complete: true}
			d.n = 0
			return u, 0
		}
		d.buf[d.n] = c
		d.n++
		b = b[1:]
	}
	r := decode(d.buf[:d.n])
	n := d.n
	d.n = 0
	if !scalar.Valid(r) {
		return Unit{Bad: true, Size: n, Complete: true}, n
	}
	return Unit{R: r, Size: n, Complete: true}, n
}

func (d *Decoder) Close() Unit {
	if d.n == 0 {
		return Unit{}
	}
	u := Unit{Bad: true, Size: d.n, Complete: true}
	d.n = 0
	return u
}

func (d *Decoder) Pending() []byte { return d.buf[:d.n] }

func Decode(b []byte) (Unit, int) {
	var d Decoder
	return d.Write(b)
}

func Length(r rune) int {
	switch {
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

func Encode(p []byte, r rune) int {
	n := Length(r)
	switch n {
	case 1:
		p[0] = byte(r)
	case 2:
		p[0] = 0xC0 | byte(r>>6)
		p[1] = 0x80 | byte(r)&0x3F
	case 3:
		p[0] = 0xE0 | byte(r>>12)
		p[1] = 0x80 | byte(r>>6)&0x3F
		p[2] = 0x80 | byte(r)&0x3F
	default:
		p[0] = 0xF0 | byte(r>>18)
		p[1] = 0x80 | byte(r>>12)&0x3F
		p[2] = 0x80 | byte(r>>6)&0x3F
		p[3] = 0x80 | byte(r)&0x3F
	}
	return n
}

func start(b byte) bool { return b >= 0xC2 && b <= 0xF4 }
func cont(b byte) bool  { return b&0xC0 == 0x80 }

func need(b byte) int {
	switch {
	case b < 0xE0:
		return 2
	case b < 0xF0:
		return 3
	default:
		return 4
	}
}

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

func decode(b []byte) rune {
	r := rune(b[0] & (0xFF >> uint(len(b))))
	for _, c := range b[1:] {
		r = r<<6 | rune(c&0x3F)
	}
	return r
}
