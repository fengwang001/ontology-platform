// Package u8 decodes and encodes UTF-8 at the byte level.
package u8

import "ontology/scalar"

// Event is one decoded unit.
type Event struct {
	R       rune // scalar when OK; 0 otherwise
	OK      bool // valid scalar vs one invalid unit
	Consume int  // bytes this unit swallowed (1..4)
}

// Decoder is a byte-at-a-time UTF-8 decoder. It never rescans buffered bytes.
type Decoder struct {
	buf [4]byte
	n   int // valid prefix length (0 at a boundary)
	need int
	r    rune
	wait bool
}

// Push feeds one byte. ok is false when the byte is only buffered (no event).
func (d *Decoder) Push(b byte) (e Event, ok bool) {
	if d.n == 0 {
		d.start(b)
	} else {
		d.more(b)
	}
	if d.wait {
		return Event{}, false
	}
	e = d.emit(b)
	d.n, d.wait = 0, false
	return e, true
}

// Close ends the stream; a dangling valid prefix is one invalid unit.
func (d *Decoder) Close() (Event, bool) {
	if d.n == 0 {
		return Event{}, false
	}
	e := Event{Consume: d.n}
	d.n, d.wait = 0, false
	return e, true
}

func (d *Decoder) start(b byte) {
	d.buf[0], d.n = b, 1
	switch {
	case b < 0x80:
		d.r, d.need, d.wait = rune(b), 1, false
	case b == 0xC0 || b == 0xC1 || b >= 0xF5:
		d.need, d.wait = 1, false
	case b < 0xE0:
		d.r, d.need, d.wait = rune(b&0x1F), 2, true
	case b < 0xF0:
		d.r, d.need, d.wait = rune(b&0x0F), 3, true
	default:
		d.r, d.need, d.wait = rune(b&0x07), 4, true
	}
}

func (d *Decoder) more(b byte) {
	d.buf[d.n] = b
	if cont(b) && d.secondOK(b) {
		d.r = d.r<<6 | rune(b&0x3F)
		d.n++
		d.wait = d.n < d.need
		return
	}
	d.wait = false // prefix ends here; b is NOT swallowed
}

func (d *Decoder) secondOK(b byte) bool {
	if !cont(b) {
		return false
	}
	if d.n != 1 {
		return true
	}
	switch d.buf[0] {
	case 0xE0:
		return b >= 0xA0
	case 0xED:
		return b <= 0x9F
	case 0xF0:
		return b >= 0x90
	case 0xF4:
		return b <= 0x8F
	}
	return true
}

func (d *Decoder) emit(failed byte) Event {
	if d.n == 1 && d.need == 1 && d.buf[0] < 0x80 {
		return Event{R: rune(d.buf[0]), OK: true, Consume: 1}
	}
	if !d.wait && d.n == d.need && scalar.IsScalar(d.r) {
		return Event{R: d.r, OK: true, Consume: d.n}
	}
	return Event{Consume: d.n}
}

func cont(b byte) bool { return b >= 0x80 && b <= 0xBF }

// Encode appends the UTF-8 encoding of r.
func Encode(p []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(p, byte(r))
	case r < 0x800:
		return append(p, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
	case r < 0x10000:
		return append(p, 0xE0|byte(r>>12), 0x80|byte(r>>6&0x3F), 0x80|byte(r&0x3F))
	default:
		return append(p, 0xF0|byte(r>>18), 0x80|byte(r>>12&0x3F),
			0x80|byte(r>>6&0x3F), 0x80|byte(r&0x3F))
	}
}

// Len is the UTF-8 encoded length of r.
func Len(r rune) int {
	switch {
	case r < 0x80:
		return 1
	case r < 0x800:
		return 2
	case r < 0x10000:
		return 3
	default:
		return 4
	}
}
