package u8

import "ontology/scalar"

type Event struct {
	R      scalar.Value
	OK     bool
	Len    int
	Offset int64
	Checks int
}

type Decoder struct {
	need, got int
	value     scalar.Value
	start     int64
}

func NewDecoder() Decoder { return Decoder{} }

func (d *Decoder) Pending() bool { return d.need > 0 }

func (d *Decoder) StepAt(b byte, off int64) (Event, Event) {
	checks := 1
	if d.need == 0 {
		switch {
		case b < 0x80:
			return Event{R: scalar.Value(b), OK: true, Len: 1, Offset: off, Checks: checks}, Event{}
		case b >= 0xC2 && b <= 0xDF:
			d.need, d.got, d.value, d.start = 1, 1, scalar.Value(b&0x1F), off
		case b >= 0xE0 && b <= 0xEF:
			d.need, d.got, d.value, d.start = 2, 1, scalar.Value(b&0x0F), off
		case b >= 0xF0 && b <= 0xF4:
			d.need, d.got, d.value, d.start = 3, 1, scalar.Value(b&0x07), off
		default:
			return Event{R: scalar.Replacement, Len: 1, Offset: off, Checks: checks}, Event{}
		}
		return Event{}, Event{}
	}

	cont := b >= 0x80 && b <= 0xBF
	validSecond := cont
	if d.got == 1 {
		switch d.need {
		case 2:
			if d.value == 0 {
				validSecond = b >= 0xA0
			} else if d.value == 0x0D {
				validSecond = b <= 0x9F
			}
		case 3:
			if d.value == 0 {
				validSecond = b >= 0x90
			} else if d.value == 4 {
				validSecond = b <= 0x8F
			}
		}
	}

	if d.got == 1 && !validSecond {
		start := d.start
		*d = Decoder{}
		first := Event{R: scalar.Replacement, Len: 1, Offset: start, Checks: checks}
		if b >= 0x80 && b <= 0xBF {
			return first, Event{R: scalar.Replacement, Len: 1, Offset: off, Checks: 0}
		}
		next, extra := d.Step(b)
		next.Checks = 0
		if next.Len == 0 {
			next = extra
		}
		return first, next
	}
	if !cont {
		length := d.got
		*d = Decoder{}
		first := Event{R: scalar.Replacement, Len: length, Offset: d.start, Checks: checks}
		next, extra := d.Step(b)
		next.Checks = 0
		if next.Len == 0 {
			return first, extra
		}
		return first, next
	}
	d.value = d.value<<6 + scalar.Value(b&0x3F)
	d.got++
	d.need--
	if d.need > 0 {
		return Event{}, Event{}
	}
	r := d.value
	start := d.start
	*d = Decoder{}
	return Event{R: r, OK: scalar.Valid(r), Len: d.got, Offset: start, Checks: checks}, Event{}
}

func (d *Decoder) Step(b byte) (Event, Event) { return d.StepAt(b, 0) }

func (d *Decoder) Close() (Event, bool) {
	if d.need == 0 {
		return Event{}, false
	}
	length := d.got
	*d = Decoder{}
	return Event{R: scalar.Replacement, Len: length, Offset: d.start, Checks: 0}, true
}

func Encode(r scalar.Value) []byte {
	switch {
	case r < 0x80:
		return []byte{byte(r)}
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r)&0x3F}
	case r < 0x10000:
		return []byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6)&0x3F, 0x80 | byte(r)&0x3F}
	default:
		return []byte{0xF0 | byte(r>>18), 0x80 | byte(r>>12)&0x3F, 0x80 | byte(r>>6)&0x3F, 0x80 | byte(r)&0x3F}
	}
}
