package u16

import "ontology/scalar"

type Endian int

const (
	Little Endian = iota
	Big
	BOM
)

type Event struct {
	R                                    scalar.Value
	Unit                                 uint16
	Size                                 int
	Valid, High, Again, BOM, OddEOF, EOF bool
}
type Decoder struct {
	order    Endian
	started  bool
	half     bool
	b0       byte
	high     scalar.Value
	haveHigh bool
	next     uint16
	haveNext bool
	checks   int64
}

func NewDecoder(e Endian) *Decoder { return &Decoder{order: e} }
func (d *Decoder) Checks() int64   { return d.checks }
func (d *Decoder) Endian() Endian  { return d.order }
func (d *Decoder) SkipBOM()        { d.started = true }

func (d *Decoder) word(a, b byte) uint16 {
	if d.order == Big {
		return uint16(a)<<8 | uint16(b)
	}
	return uint16(b)<<8 | uint16(a)
}

func (d *Decoder) Feed(p []byte) Event {
	if d.haveNext {
		u := d.next
		d.next, d.haveNext = 0, false
		return d.code(u, 2)
	}
	if len(p) == 0 {
		return Event{EOF: true}
	}
	if d.half {
		b := p[0]
		d.checks++
		d.half = false
		return d.code(d.word(d.b0, b), 2)
	}
	if len(p) == 1 {
		d.checks++
		d.half, d.b0 = true, p[0]
		return Event{EOF: true}
	}
	d.checks += 2
	return d.code(d.word(p[0], p[1]), 2)
}

func (d *Decoder) code(u uint16, n int) Event {
	r := scalar.Value(u)
	if !d.started && u == 0xfeff {
		d.started = true
		return Event{Unit: u, Size: n, BOM: true}
	}
	if !d.started && d.order == BOM {
		d.started = true
		if u == 0xfffe {
			d.order = Big
			return Event{Unit: u, Size: n, BOM: true}
		}
	}
	d.started = true
	if d.haveHigh {
		h := d.high
		d.high, d.haveHigh = 0, false
		if scalar.LowSurrogate(r) {
			return Event{R: scalar.FromSurrogatePair(uint16(h), uint16(r)), Size: n, Valid: true}
		}
		d.next, d.haveNext = u, true
		return Event{Size: 2, Again: true}
	}
	if scalar.HighSurrogate(r) {
		d.high, d.haveHigh = r, true
		return Event{R: r, Size: n, High: true}
	}
	return Event{R: r, Unit: u, Size: n, Valid: scalar.Valid(r)}
}

func (d *Decoder) Close() Event {
	switch {
	case d.half:
		d.half = false
		return Event{Size: 1, OddEOF: true}
	case d.haveHigh:
		d.high, d.haveHigh = 0, false
		return Event{Size: 2}
	default:
		return Event{EOF: true}
	}
}
func (d *Decoder) Pending() int {
	if d.half {
		return 1
	}
	if d.haveHigh {
		return 2
	}
	return 0
}

func Encode(r scalar.Value, e Endian) []byte {
	if r < 0x10000 {
		return put(nil, uint16(r), e)
	}
	r -= 0x10000
	out := put(nil, uint16(0xd800+r>>10), e)
	return put(out, uint16(0xdc00+r&0x3ff), e)
}
func put(p []byte, u uint16, e Endian) []byte {
	if e == Big {
		return append(p, byte(u>>8), byte(u))
	}
	return append(p, byte(u), byte(u>>8))
}
