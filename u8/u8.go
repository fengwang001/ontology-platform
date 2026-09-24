package u8

import "ontology/scalar"

type Unit struct {
	R       rune
	Len     int
	Valid   bool
	Pending bool
	Replay  byte
}

type Decoder struct {
	buf    [3]byte
	need   int
	checks int
}

func NewDecoder() *Decoder { return &Decoder{} }

func (d *Decoder) Checks() int { return d.checks }

func (d *Decoder) Buffered() int {
	if d.need == 0 {
		return 0
	}
	return 4 - d.need
}

func (d *Decoder) Reset() { d.need = 0 }

func (d *Decoder) Prefix() []byte {
	n := d.Buffered()
	return append([]byte(nil), d.buf[:n]...)
}

func (d *Decoder) EOF() Unit {
	if d.Buffered() == 0 {
		return Unit{}
	}
	length := d.Buffered()
	d.need = 0
	return Unit{Len: length}
}

func (d *Decoder) Push(b byte) Unit {
	d.checks++
	if d.need == 0 {
		return d.start(b)
	}
	first := d.buf[0]
	atSecond := d.Buffered() == 1
	if b < 0x80 || b > 0xBF || (atSecond && !secondOK(first, b)) {
		length := d.Buffered()
		d.need = 0
		return Unit{Len: length, Replay: b}
	}
	n := d.Buffered()
	d.buf[n] = b
	if n+1 < d.need-1 {
		return Unit{Pending: true}
	}
	r := decode(first, d.buf[:d.need-1])
	length := d.need
	d.need = 0
	return Unit{R: r, Len: length, Valid: scalar.Valid(r)}
}

func (d *Decoder) start(b byte) Unit {
	switch {
	case b < 0x80:
		return Unit{R: rune(b), Len: 1, Valid: true}
	case b < 0xC2:
		return Unit{Len: 1}
	case b < 0xE0:
		d.need, d.buf[0] = 2, b
	case b < 0xF0:
		d.need, d.buf[0] = 3, b
	case b < 0xF5:
		d.need, d.buf[0] = 4, b
	default:
		return Unit{Len: 1}
	}
	return Unit{Pending: true}
}

func secondOK(first, b byte) bool {
	switch first {
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

func decode(first byte, rest []byte) rune {
	r := rune(first)
	switch len(rest) + 1 {
	case 2:
		r = (r&0x1f)<<6 | rune(rest[0]&0x3f)
	case 3:
		r = (r&0x0f)<<12 | rune(rest[0]&0x3f)<<6 | rune(rest[1]&0x3f)
	default:
		r = (r&0x07)<<18 | rune(rest[0]&0x3f)<<12 | rune(rest[1]&0x3f)<<6 | rune(rest[2]&0x3f)
	}
	return r
}

func EncodeAppend(out []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(out, byte(r))
	case r < 0x800:
		return append(out, 0xC0|byte(r>>6), 0x80|byte(r&0x3f))
	case r < 0x10000:
		return append(out, 0xE0|byte(r>>12), 0x80|byte(r>>6&0x3f), 0x80|byte(r&0x3f))
	default:
		return append(out, 0xF0|byte(r>>18), 0x80|byte(r>>12&0x3f), 0x80|byte(r>>6&0x3f), 0x80|byte(r&0x3f))
	}
}
