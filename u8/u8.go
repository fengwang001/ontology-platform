package u8

import "ontology/scalar"

type EventKind int

const (
	Scalar EventKind = iota
	Invalid
	Truncated
)

type Event struct {
	Kind  EventKind
	Rune  rune
	Bytes int
}

type Decoder struct {
	first byte
	need, total, value int
	checks             int64
}

func (d *Decoder) Pending() int { return d.need }

func (d *Decoder) Reset() { d.need, d.value = 0, 0 }

func (d *Decoder) Checks() int64 { return d.checks }

func (d *Decoder) Feed(p []byte, eof bool) (Event, int) {
	consumed := 0
	if d.need == 0 {
		if len(p) == 0 {
			return Event{}, 0
		}
		d.checks++
		b := p[0]
		switch {
		case b < 0x80:
			return Event{Kind: Scalar, Rune: rune(b), Bytes: 1}, 1
		case b < 0xC2, b > 0xF4:
			return Event{Kind: Invalid, Rune: scalar.Replacement, Bytes: 1}, 1
		case b < 0xE0:
			d.total, d.need, d.value = 2, 1, int(b&0x1F)
		case b < 0xF0:
			d.total, d.need, d.value = 3, 2, int(b&0x0F)
		default:
			d.total, d.need, d.value = 4, 3, int(b&0x07)
		}
		d.first, consumed = b, 1
		if len(p) == 1 {
			return d.partial(eof, consumed)
		}
	}
	for d.need > 0 && consumed < len(p) {
		d.checks++
		b, have := p[consumed], d.total-d.need
		valid := b >= 0x80 && b <= 0xBF
		if have == 1 {
			switch d.first {
			case 0xE0:
				valid = b >= 0xA0 && b <= 0xBF
			case 0xED:
				valid = b >= 0x80 && b <= 0x9F
			case 0xF0:
				valid = b >= 0x90 && b <= 0xBF
			case 0xF4:
				valid = b >= 0x80 && b <= 0x8F
			}
		}
		if !valid {
			n := have + 1
			d.Reset()
			return Event{Kind: Invalid, Rune: scalar.Replacement, Bytes: n}, consumed + 1
		}
		d.value, d.need, consumed = d.value<<6|int(b&0x3F), d.need-1, consumed+1
		if d.need == 0 {
			d.need = 0
			return Event{Kind: Scalar, Rune: rune(d.value), Bytes: d.total}, consumed
		}
	}
	return d.partial(eof, consumed)
}

func (d *Decoder) partial(eof bool, consumed int) (Event, int) {
	if eof && d.need > 0 {
		n := d.total - d.need
		d.Reset()
		return Event{Kind: Truncated, Rune: scalar.Replacement, Bytes: n}, consumed
	}
	return Event{}, consumed
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
