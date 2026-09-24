package u8

import "ontology/scalar"

const BOM = 0xFEFF

type Event struct {
	R       rune
	Invalid bool
	Size    int
	BOM     bool
}

type Decoder struct {
	buf        [4]byte
	have, need int
	started    bool
	bom        [3]byte
	bomHave    int
	checks     int64
}

func (d *Decoder) Reset() { *d = Decoder{} }
func (d *Decoder) Checks() int64 { return d.checks }

func isCont(b byte) bool { return b&0xC0 == 0x80 }

func leadNeed(b byte) int {
	switch {
	case b >= 0xC2 && b <= 0xDF:
		return 2
	case b >= 0xE0 && b <= 0xEF:
		return 3
	case b >= 0xF0 && b <= 0xF4:
		return 4
	}
	return 1
}

func secondOK(first, second byte) bool {
	switch {
	case first == 0xE0:
		return second >= 0xA0 && second <= 0xBF
	case first == 0xED:
		return second >= 0x80 && second <= 0x9F
	case first == 0xF0:
		return second >= 0x90 && second <= 0xBF
	case first == 0xF4:
		return second >= 0x80 && second <= 0x8F
	default:
		return isCont(second)
	}
}

func firstValue(b byte) rune {
	switch {
	case b < 0x80:
		return rune(b)
	case b < 0xE0:
		return rune(b & 0x1F)
	case b < 0xF0:
		return rune(b & 0x0F)
	default:
		return rune(b & 0x07)
	}
}

func (d *Decoder) Accept(b byte) (Event, bool) {
	d.checks++
	if !d.started {
		return d.acceptStart(b)
	}
	if d.need == 0 {
		return d.begin(b)
	}
	return d.cont(b)
}

func (d *Decoder) acceptStart(b byte) (Event, bool) {
	want := [3]byte{0xEF, 0xBB, 0xBF}
	if d.bomHave < 3 && b == want[d.bomHave] {
		d.bom[d.bomHave] = b
		d.bomHave++
		if d.bomHave == 3 {
			d.started, d.bomHave = true, 0
			return Event{R: BOM, Size: 3, BOM: true}, true
		}
		return Event{}, false
	}
	pending := append(append([]byte(nil), d.bom[:d.bomHave]...), b)
	d.started, d.bomHave = true, 0
	for _, next := range pending {
		if d.need == 0 {
			if event, ok := d.begin(next); ok {
				return event, true
			}
		} else {
			if event, ok := d.cont(next); ok {
				return event, true
			}
		}
	}
	return Event{}, false
}

func (d *Decoder) begin(b byte) (Event, bool) {
	if b < 0x80 {
		return Event{R: rune(b), Size: 1}, true
	}
	if b < 0xC2 || b > 0xF4 {
		return Event{R: scalar.Replacement, Invalid: true, Size: 1}, true
	}
	d.buf[0], d.have, d.need = b, 1, leadNeed(b)
	return Event{}, false
}

func (d *Decoder) cont(b byte) (Event, bool) {
	if !isCont(b) || (d.have == 1 && !secondOK(d.buf[0], b)) {
		size := d.have
		d.have, d.need = 0, 0
		return Event{R: scalar.Replacement, Invalid: true, Size: size}, true
	}
	d.buf[d.have] = b
	d.have++
	if d.have != d.need {
		return Event{}, false
	}
	r := firstValue(d.buf[0])
	for _, next := range d.buf[1:d.have] {
		r = r<<6 | rune(next&0x3F)
	}
	size := d.have
	d.have, d.need = 0, 0
	return Event{R: r, Size: size}, true
}

func (d *Decoder) End() (Event, bool) {
	if d.bomHave > 0 {
		size, d.bomHave = d.bomHave, 0
		return Event{R: scalar.Replacement, Invalid: true, Size: size}, true
	}
	if d.need == 0 {
		return Event{}, false
	}
	size := d.have
	d.have, d.need = 0, 0
	return Event{R: scalar.Replacement, Invalid: true, Size: size}, true
}

func Encode(r rune) ([]byte, bool) {
	if !scalar.IsScalar(r) {
		return nil, false
	}
	switch {
	case r < 0x80:
		return []byte{byte(r)}, true
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r)&0x3F}, true
	case r < 0x10000:
		return []byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6)&0x3F, 0x80 | byte(r)&0x3F}, true
	default:
		return []byte{byte(0xF0 | r>>18), byte(0x80 | r>>12&0x3F), byte(0x80 | r>>6&0x3F), byte(0x80 | r&0x3F)}, true
	}
}

func AlignStart(data []byte, at int) int {
	if at <= 0 || at >= len(data) {
		return at
	}
	for i := at - 1; i >= 0 && i >= at-3; i-- {
		b := data[i]
		if b < 0xC2 || b > 0xF4 {
			return at
		}
		if at < i+leadNeed(b) {
			return i
		}
	}
	return at
}
