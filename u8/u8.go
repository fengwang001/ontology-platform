package u8

import "ontology/scalar"

type Event struct {
	R       rune
	Size    int
	Valid   bool
	Partial bool
	Follow  *Event
}

type Decoder struct {
	first byte
	r     rune
	need  int
	got   int
}

func (d *Decoder) Partial() bool { return d.got > 0 }
func (d *Decoder) Pending() int  { return d.got }

func (d *Decoder) Step(b byte) Event {
	if d.got == 0 {
		d.need = scalar.LeadSize(b)
		if d.need == 0 {
			return Event{R: scalar.Replacement, Size: 1}
		}
		d.first = b
		d.got = 1
		d.r = rune(b) & (0x7f >> uint(d.need))
		if d.need == 1 {
			d.got = 0
			return Event{R: rune(b), Size: 1, Valid: true}
		}
		return Event{}
	}

	d.got++
	invalidSecond := d.got == 2 && !scalar.SecondOK(d.first, b)
	invalidTail := d.got > 2 && !scalar.Continuation(b)
	if invalidSecond || invalidTail {
		size := d.got - 1
		d.got = 0
		e := Event{R: scalar.Replacement, Size: size}
		if follow := d.Step(b); follow.Size != 0 {
			e.Follow = &follow
		}
		return e
	}

	d.r = d.r<<6 | rune(b&0x3f)
	if d.got != d.need {
		return Event{}
	}
	r, size := d.r, d.need
	d.got = 0
	if !scalar.Valid(r) {
		return Event{R: scalar.Replacement, Size: size}
	}
	return Event{R: r, Size: size, Valid: true}
}

func (d *Decoder) End() Event {
	if d.got == 0 {
		return Event{}
	}
	size := d.got
	d.got = 0
	return Event{R: scalar.Replacement, Size: size, Partial: true}
}

func DecodeAt(p []byte) Event {
	var d Decoder
	var last Event
	for _, b := range p {
		last = d.Step(b)
	}
	if len(p) > 0 && last.Size == 0 {
		last = d.End()
	}
	return last
}

func Append(dst []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r&0x3f))
	case r < 0x10000:
		return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3f, 0x80|byte(r&0x3f))
	default:
		return append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12)&0x3f,
			0x80|byte(r>>6)&0x3f, 0x80|byte(r&0x3f))
	}
}

func EncodedLen(r rune) int {
	if r < 0x80 {
		return 1
	}
	if r < 0x800 {
		return 2
	}
	if r < 0x10000 {
		return 3
	}
	return 4
}
