package u8

import "ontology/scalar"

type Decoder struct {
	pending [3]byte
	n       int
}

func (d *Decoder) Pending() int {
	return d.n
}

func (d *Decoder) Step(b byte) (r rune, size int, invalid, done bool) {
	n := d.Pending()
	if n == 0 {
		switch {
		case b < 0x80:
			return rune(b), 1, false, true
		case b >= 0xC2 && b <= 0xDF:
			d.n = 1
			d.pending[0] = b
		case b >= 0xE0 && b <= 0xEF || b >= 0xF0 && b <= 0xF4:
			d.n = 1
			d.pending[0] = b
		default:
			return scalar.Replacement, 1, true, true
		}
		return 0, 0, false, false
	}
	first := d.pending[0]
	if b < 0x80 || b > 0xBF || n == 1 && !secondOK(first, b) {
		size = n + 1
		d.n = 0
		return scalar.Replacement, size, true, true
	}
	d.pending[n] = b
	want := 2
	if first >= 0xE0 {
		want = 3
	}
	if first >= 0xF0 {
		want = 4
	}
	if n+1 < want {
		return 0, 0, false, false
	}
	r = decode(d.pending[:want])
	d.n = 0
	return r, want, false, true
}

func (d *Decoder) EOF() (r rune, size int, invalid bool, ok bool) {
	n := d.Pending()
	if n == 0 {
		return 0, 0, false, false
	}
	d.n = 0
	return scalar.Replacement, n, true, true
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

func decode(p []byte) rune {
	r := rune(p[0]) & (1<<(7-len(p)) - 1)
	for _, b := range p[1:] {
		r = r<<6 | rune(b&0x3F)
	}
	return r
}

func Encode(r rune) []byte {
	switch {
	case r < 0x80:
		return []byte{byte(r)}
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r&0x3F)}
	case r < 0x10000:
		return []byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6&0x3F), 0x80 | byte(r&0x3F)}
	default:
		return []byte{0xF0 | byte(r>>18), 0x80 | byte(r>>12&0x3F), 0x80 | byte(r>>6&0x3F), 0x80 | byte(r&0x3F)}
	}
}
