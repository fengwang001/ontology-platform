package u8

import "ontology/scalar"

type Event struct {
	R     scalar.Value
	Size  int
	Valid bool
	Again bool
	EOF   bool
}

type Decoder struct {
	state  int
	need   int
	size   int
	value  scalar.Value
	checks int64
}

func (d *Decoder) Checks() int64 { return d.checks }

func continuation(b byte) bool { return b&0xc0 == 0x80 }

func LeadLen(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b >= 0xc2 && b <= 0xdf:
		return 2
	case b >= 0xe0 && b <= 0xef:
		return 3
	case b >= 0xf0 && b <= 0xf4:
		return 4
	default:
		return 1
	}
}

func (d *Decoder) Feed(p []byte) Event {
	for len(p) > 0 {
		b := p[0]
		d.checks++
		if d.state == 0 {
			switch {
			case b < 0x80:
				return Event{R: scalar.Value(b), Size: 1, Valid: true}
			case b >= 0xc2 && b <= 0xdf:
				d.state, d.need, d.size = 1, 1, 2
				d.value = scalar.Value(b & 0x1f)
			case b == 0xe0:
				d.state, d.need, d.size = 2, 2, 3
				d.value = 0
			case b >= 0xe1 && b <= 0xec:
				d.state, d.need, d.size = 1, 2, 3
				d.value = scalar.Value(b & 0x0f)
			case b == 0xed:
				d.state, d.need, d.size = 3, 2, 3
				d.value = 0
			case b >= 0xee && b <= 0xef:
				d.state, d.need, d.size = 1, 2, 3
				d.value = scalar.Value(b & 0x0f)
			case b == 0xf0:
				d.state, d.need, d.size = 4, 3, 4
				d.value = 0
			case b >= 0xf1 && b <= 0xf3:
				d.state, d.need, d.size = 1, 3, 4
				d.value = scalar.Value(b & 0x07)
			case b == 0xf4:
				d.state, d.need, d.size = 5, 3, 4
				d.value = 0
			default:
				return Event{Size: 1}
			}
			p = p[1:]
			continue
		}
		ok := continuation(b)
		d.checks++
		if d.state == 2 {
			ok = ok && b >= 0xa0
		} else if d.state == 3 {
			ok = ok && b <= 0x9f
		} else if d.state == 4 {
			ok = ok && b >= 0x90
		} else if d.state == 5 {
			ok = ok && b <= 0x8f
		}
		if !ok {
			size := d.size - d.need
			d.ResetPrefix()
			return Event{Size: size, Again: true}
		}
		d.value = d.value<<6 | scalar.Value(b&0x3f)
		d.need--
		d.state = 1
		if d.need == 0 {
			r := d.value
			size := d.size
			d.ResetPrefix()
			return Event{R: r, Size: size, Valid: scalar.Valid(r)}
		}
		p = p[1:]
	}
	return Event{EOF: true}
}

func (d *Decoder) Close() Event {
	if d.need == 0 {
		return Event{EOF: true}
	}
	size := d.size - d.need
	d.ResetPrefix()
	return Event{Size: size, EOF: true}
}

func (d *Decoder) Pending() int { return d.size - d.need }
func (d *Decoder) ResetPrefix() { d.state, d.need, d.size, d.value = 0, 0, 0, 0 }

func Encode(r scalar.Value) []byte {
	switch {
	case r <= 0x7f:
		return []byte{byte(r)}
	case r <= 0x7ff:
		return []byte{0xc0 | byte(r>>6), 0x80 | byte(r&0x3f)}
	case r <= 0xffff:
		return []byte{0xe0 | byte(r>>12), 0x80 | byte(r>>6&0x3f), 0x80 | byte(r&0x3f)}
	default:
		return []byte{0xf0 | byte(r>>18), 0x80 | byte(r>>12&0x3f), 0x80 | byte(r>>6&0x3f), 0x80 | byte(r&0x3f)}
	}
}
