package u16

import "ontology/scalar"

type Endian uint8

const (
	Little Endian = iota
	Big
	Auto
)

const MaxCache = 3

type Kind uint8

const (
	OK Kind = iota
	Invalid
	Truncated
	BOMKind
)

type Unit struct {
	Kind  Kind
	R     scalar.Rune
	Start int64
	Size  int
}

type Decoder struct {
	order     Endian
	pending   byte
	haveByte  bool
	highStart int64
	high      scalar.Rune
	haveHigh  bool
	consumed  int64
	checks    int64
}

func NewDecoder(order Endian) *Decoder { return &Decoder{order: order} }

func (d *Decoder) Feed(p []byte) []Unit {
	var units []Unit
	for _, b := range p {
		d.checks++
		d.consumed++
		if !d.haveByte {
			d.pending, d.haveByte = b, true
			continue
		}
		d.haveByte = false
		var v scalar.Rune
		if d.order == Big {
			v = scalar.Rune(d.pending)<<8 | scalar.Rune(b)
		} else {
			v = scalar.Rune(b)<<8 | scalar.Rune(d.pending)
		}
		units = d.unit(units, v, d.consumed-2)
	}
	return units
}

func (d *Decoder) unit(units []Unit, v scalar.Rune, start int64) []Unit {
	if d.consumed == 2 && d.order == Auto {
		switch v {
		case 0xFEFF:
			d.order = Little
			return append(units, Unit{BOMKind, v, start, 2})
		case 0xFFFE:
			d.order = Big
			return append(units, Unit{BOMKind, scalar.BOM, start, 2})
		}
		d.order = Little
	}
	if d.haveHigh {
		if scalar.IsLowSurrogate(v) {
			r := 0x10000 + (d.high-0xD800)<<10 + (v - 0xDC00)
			d.haveHigh = false
			return append(units, Unit{OK, r, d.highStart, 4})
		}
		units = append(units, Unit{Invalid, 0, d.highStart, 2})
		d.haveHigh = false
	}
	switch {
	case scalar.IsHighSurrogate(v):
		d.haveHigh, d.highStart, d.high = true, start, v
	case scalar.IsLowSurrogate(v):
		units = append(units, Unit{Invalid, 0, start, 2})
	default:
		units = append(units, Unit{OK, v, start, 2})
	}
	return units
}

func (d *Decoder) Flush() (Unit, bool) {
	if d.haveByte {
		u := Unit{Kind: Truncated, Start: d.consumed - 1, Size: 1}
		d.haveByte = false
		return u, true
	}
	if d.haveHigh {
		u := Unit{Kind: Truncated, Start: d.highStart, Size: 2}
		d.haveHigh = false
		return u, true
	}
	return Unit{}, false
}

func (d *Decoder) Checks() int64 { return d.checks }
func (d *Decoder) Order() Endian { return d.order }
func (d *Decoder) Pending() int {
	if d.haveByte {
		return 1
	}
	if d.haveHigh {
		return 2
	}
	return 0
}

func Encode(p []byte, r scalar.Rune, order Endian) []byte {
	if r > 0xFFFF {
		r -= 0x10000
		p = encode16(p, 0xD800+(r>>10), order)
		r = 0xDC00 + (r & 0x3FF)
	}
	return encode16(p, r, order)
}

func encode16(p []byte, v scalar.Rune, order Endian) []byte {
	hi, lo := byte(v>>8), byte(v)
	if order == Big {
		return append(p, hi, lo)
	}
	return append(p, lo, hi)
}
