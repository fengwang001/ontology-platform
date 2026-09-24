package u16

import "ontology/scalar"

type Endian int

const (
	Little Endian = iota
	Big
)

type Unit struct {
	R       rune
	Size    int
	Legal   bool
	Partial bool
}

type Decoder struct {
	endian Endian
	first  byte
	has    bool
	high   rune
	hasHi  bool
}

func NewDecoder(endian Endian) *Decoder { return &Decoder{endian: endian} }

func (d *Decoder) SetEndian(endian Endian) { d.endian = endian }

func (d *Decoder) Pending() []byte {
	if !d.has {
		return nil
	}
	return []byte{d.first}
}

func (d *Decoder) InUnit() bool { return d.has || d.hasHi }

func (d *Decoder) Feed(b byte) (Unit, bool, bool) {
	if !d.has {
		d.first, d.has = 0, false
		return Unit{}, false, true
	}
	var word rune
	if d.endian == Little {
		word = rune(d.first) | rune(b)<<8
	} else {
		word = rune(b) | rune(d.first)<<8
	}
	d.has = false
	if d.hasHi {
		d.hasHi = false
		if scalar.LowSurrogate(word) {
			r := 0x10000 + (d.high-scalar.HighMin)<<10 + (word - scalar.LowMin)
			return Unit{R: r, Size: 4, Legal: true}, true, true
		}
		d.first, d.has = b, true
		return Unit{Size: 2, Partial: true}, true, false
	}
	switch {
	case scalar.HighSurrogate(word):
		d.high, d.hasHi = word, true
		return Unit{}, false, true
	case scalar.LowSurrogate(word):
		return Unit{Size: 2, Partial: true}, true, true
	default:
		return Unit{R: word, Size: 2, Legal: true}, true, true
	}
}

func (d *Decoder) Close() Unit {
	if d.hasHi {
		d.hasHi = false
		return Unit{Size: 2, Partial: true}
	}
	if d.has {
		d.has = false
		return Unit{Size: 1, Partial: true}
	}
	return Unit{}
}

func Encode(r rune, endian Endian) []byte {
	if !scalar.Valid(r) {
		r = scalar.Replacement
	}
	if r < 0x10000 {
		return word(uint16(r), endian)
	}
	v := uint32(r) - 0x10000
	hi := uint16(scalar.HighMin + v/0x400)
	lo := uint16(scalar.LowMin + v%0x400)
	return append(word(hi, endian), word(lo, endian)...)
}

const Replacement = scalar.Replacement

func word(v uint16, endian Endian) []byte {
	if endian == Little {
		return []byte{byte(v), byte(v >> 8)}
	}
	return []byte{byte(v >> 8), byte(v)}
}
