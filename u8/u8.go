// Package u8 decodes and encodes UTF-8 byte sequences one scalar at a time.
package u8

import "ontology/scalar"

// Replacement is the U+FFFD rune.
const Replacement rune = 0xFFFD

// Unit is one decoded result: a valid scalar or one illegal unit.
type Unit struct {
	R     rune // scalar when !Bad; otherwise -1
	Bad   bool
	N     int  // source bytes this unit accounts for (1..4)
	Trunc bool // unit is an unfinished prefix reported at end of input
}

// Decoder is an incremental, split-safe UTF-8 decoder.
type Decoder struct {
	pend   int  // buffered bytes of the in-progress prefix (0..3)
	need   int  // total length expected for the prefix
	cp     rune // accumulated code point bits
	checks int64
}

// NewDecoder returns an empty decoder.
func NewDecoder() *Decoder { return &Decoder{} }

// Checks reports how many byte examinations have happened.
func (d *Decoder) Checks() int64 { return d.checks }

// Pending reports how many bytes are buffered (<=3).
func (d *Decoder) Pending() int { return d.pend }

// LeadLen returns the expected length of a UTF-8 sequence starting with b
// and false when b is not a multi-byte lead.
func LeadLen(b byte) (int, bool) {
	switch {
	case b >= 0xC2 && b <= 0xDF:
		return 2, true
	case b >= 0xE0 && b <= 0xEF:
		return 3, true
	case b >= 0xF0 && b <= 0xF4:
		return 4, true
	default:
		return 0, false
	}
}

func bad(n int) Unit { return Unit{R: -1, Bad: true, N: n} }

// Feed examines one input byte and returns zero or more finished units.
// Sum of the returned units' N equals the bytes now committed; an unfinished
// prefix keeps its byte buffered.
func (d *Decoder) Feed(b byte) []Unit {
	d.checks++
	if d.pend == 0 {
		return d.start(b)
	}
	if b < 0x80 || b > 0xBF { // not a continuation: prefix dies, re-parse b
		u := bad(d.pend)
		d.pend = 0
		return append([]Unit{u}, d.start(b)...)
	}
	if d.pend == 1 { // second byte: lead-specific legal range
		ok := true
		switch lead := byte(d.cp); {
		case lead == 0xE0:
			ok = b >= 0xA0
		case lead == 0xED:
			ok = b <= 0x9F
		case lead == 0xF0:
			ok = b >= 0x90
		case lead == 0xF4:
			ok = b <= 0x8F
		}
		if !ok { // lead alone is one bad unit; this byte is re-parsed
			d.pend = 0
			return append([]Unit{bad(1)}, d.start(b)...)
		}
	}
	d.cp = d.cp<<6 | rune(b&0x3F)
	d.pend++
	if d.pend == d.need {
		r := d.cp
		n := d.need
		d.pend, d.cp, d.need = 0, 0, 0
		if !scalar.Valid(r) {
			return []Unit{bad(n)}
		}
		return []Unit{{R: r, N: n}}
	}
	return nil
}

func (d *Decoder) start(b byte) []Unit {
	switch {
	case b < 0x80:
		return []Unit{{R: rune(b), N: 1}}
	case b >= 0xC2 && b <= 0xDF:
		d.pend, d.need, d.cp = 1, 2, rune(b&0x1F)
	case b >= 0xE0 && b <= 0xEF:
		d.pend, d.need, d.cp = 1, 3, rune(b&0x0F)
	case b >= 0xF0 && b <= 0xF4:
		d.pend, d.need, d.cp = 1, 4, rune(b&0x07)
	default: // 80..BF, C0, C1, F5..FF
		return []Unit{bad(1)}
	}
	return nil
}

// Finish must be called at end of input. A buffered prefix becomes one
// truncated bad unit.
func (d *Decoder) Finish() []Unit {
	if d.pend == 0 {
		return nil
	}
	u := bad(d.pend)
	u.Trunc = true
	d.pend = 0
	return []Unit{u}
}

// Encode appends the UTF-8 encoding of scalar r to dst.
func Encode(dst []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
	case r < 0x10000:
		return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r&0x3F))
	default:
		return append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12)&0x3F,
			0x80|byte(r>>6)&0x3F, 0x80|byte(r&0x3F))
	}
}
