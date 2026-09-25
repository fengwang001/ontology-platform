package u16

import "ontology/scalar"

type Decoder struct {
	order       binary
	first       byte
	haveByte    bool
	high        uint16
	haveHigh    bool
	queue       [2]uint16
	qn          int
}

type binary bool

const LE binary = false
const BE binary = true

func NewDecoder(bo binary) *Decoder { return &Decoder{order: bo} }

func (d *Decoder) Step(b byte) (r rune, size int, invalid bool, ok bool) {
	if !d.haveByte {
		d.first, d.haveByte = b, true
		return
	}
	code := uint16(b)
	if d.order == BE {
		code = uint16(d.first)<<8 | code
	} else {
		code = uint16(b)<<8 | uint16(d.first)
	}
	d.haveByte = false
	if d.haveHigh {
		switch {
		case scalar.LowSurrogate(d.queue[0]) && d.qn > 0:
			low := d.queue[0]
			d.shift()
			d.haveHigh = false
			return 0x10000 + rune(d.high-0xD800)<<10 + rune(low-0xDC00), 4, false, true
		default:
			d.haveHigh = false
			d.shift()
			return scalar.Replacement, 2, true, true
		}
	}
	d.queue[d.qn] = code
	d.qn++
	return d.code(d.queue[0])
}

func (d *Decoder) shift() {
	copy(d.queue[:], d.queue[1:d.qn])
	d.qn--
}

func (d *Decoder) code(code uint16) (r rune, size int, invalid bool, ok bool) {
	switch {
	case scalar.HighSurrogate(code):
		d.high, d.haveHigh = code, true
		d.shift()
		return 0, 0, false, false
	case scalar.LowSurrogate(code):
		d.shift()
		return scalar.Replacement, 2, true, true
	default:
		d.shift()
		return rune(code), 2, false, true
	}
}

func (d *Decoder) EOF() (r rune, size int, invalid bool, ok bool) {
	if d.haveByte {
		d.haveByte = false
		return scalar.Replacement, 1, true, true
	}
	if d.haveHigh {
		d.haveHigh = false
		return scalar.Replacement, 2, true, true
	}
	if d.qn > 0 {
		return d.code(d.queue[0])
	}
	return 0, 0, false, false
}

func Encode(r rune, bo binary) []byte {
	if r < 0x10000 {
		return codeUnit(uint16(r), bo)
	}
	r -= 0x10000
	return append(codeUnit(0xD800+uint16(r>>10), bo), codeUnit(0xDC00+uint16(r&0x3FF), bo)...)
}

func codeUnit(u uint16, bo binary) []byte {
	if bo == BE {
		return []byte{byte(u >> 8), byte(u)}
	}
	return []byte{byte(u), byte(u >> 8)}
}
