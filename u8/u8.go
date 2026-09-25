// Package u8 decodes and encodes UTF-8 byte sequences one scalar (or one
// illegal unit) at a time, carrying an incomplete prefix across calls.
package u8

import "ontology/scalar"

// MaxPending is the hard cap on buffered input bytes: a 4-byte character's
// lead plus at most three continuation bytes.
const MaxPending = 3

// Unit is one decoded outcome. Len 0 means the input ended in the middle of a
// still-legal prefix; more bytes are required.
type Unit struct {
	R   scalar.Rune
	Len int
	Bad bool
}

// Decoder is a stateful UTF-8 reader. A single instance is not safe for
// concurrent use.
type Decoder struct {
	pend   [4]byte
	n      int
	// Checks counts every byte examination, including re-examination of a
	// byte that was rejected as a continuation and held back.
	Checks int64
}

// PendingLen reports how many unresolvable prefix bytes are buffered.
func (d *Decoder) PendingLen() int { return d.n }

// Pending returns the buffered prefix.
func (d *Decoder) Pending() []byte { return d.pend[:d.n] }

// Feed consumes bytes from p and returns the first resolved unit. The returned
// int is the number of bytes taken from p (the held-back next lead excluded).
func (d *Decoder) Feed(p []byte) (Unit, int) {
	used := 0
	if d.n == 0 {
		if len(p) == 0 {
			return Unit{}, 0
		}
		b := p[0]
		used = 1
		d.Checks++
		d.pend[0] = b
		d.n = 1
		switch {
		case b < 0x80:
			return d.finish(Unit{R: scalar.Rune(b), Len: 1}), used
		case b < 0xC2: // 80..BF stray continuation, C0 C1 never minimal
			return d.finish(Unit{Len: 1, Bad: true}), used
		case b <= 0xDF:
		case b == 0xE0 || (b >= 0xE1 && b <= 0xEC) || b == 0xED || (b >= 0xEE && b <= 0xEF):
		case b == 0xF0 || (b >= 0xF1 && b <= 0xF3) || b == 0xF4:
		default: // F5..FF
			return d.finish(Unit{Len: 1, Bad: true}), used
		}
	}
	lead := d.pend[0]
	need := 2
	if lead >= 0xE0 {
		need = 3
	}
	if lead >= 0xF0 {
		need = 4
	}
	for d.n < need {
		if used >= len(p) {
			return Unit{}, used
		}
		b := p[used]
		d.Checks++
		lo, hi := 0x80, 0xBF
		if d.n == 1 {
			switch lead {
			case 0xE0:
				lo = 0xA0
			case 0xED:
				hi = 0x9F
			case 0xF0:
				lo = 0x90
			case 0xF4:
				hi = 0x8F
			}
		}
		if b < byte(lo) || b > byte(hi) {
			return d.finish(Unit{Len: d.n, Bad: true}), used
		}
		d.pend[d.n] = b
		d.n++
		used++
	}
	return d.finish(decode(d.pend[:need])), used
}

func (d *Decoder) finish(u Unit) Unit {
	d.n = 0
	return u
}

func decode(b []byte) Unit {
	var r scalar.Rune
	switch len(b) {
	case 2:
		r = scalar.Rune(b[0]&0x1F)<<6 | scalar.Rune(b[1]&0x3F)
	case 3:
		r = scalar.Rune(b[0]&0x0F)<<12 | scalar.Rune(b[1]&0x3F)<<6 | scalar.Rune(b[2]&0x3F)
	default:
		r = scalar.Rune(b[0]&0x07)<<18 | scalar.Rune(b[1]&0x3F)<<12 |
			scalar.Rune(b[2]&0x3F)<<6 | scalar.Rune(b[3]&0x3F)
	}
	if !scalar.Scalar(r) {
		return Unit{Len: len(b), Bad: true}
	}
	return Unit{R: r, Len: len(b)}
}

// Flush resolves a prefix left at end of input: one illegal unit if anything
// is buffered, otherwise nothing.
func (d *Decoder) Flush() Unit {
	if d.n == 0 {
		return Unit{}
	}
	u := Unit{Len: d.n, Bad: true}
	d.n = 0
	return u
}

// Encode appends the UTF-8 encoding of r to dst.
func Encode(dst []byte, r scalar.Rune) []byte {
	switch {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
	case r < 0x10000:
		return append(dst, 0xE0|byte(r>>12),
			0x80|byte((r>>6)&0x3F), 0x80|byte(r&0x3F))
	default:
		return append(dst, 0xF0|byte(r>>18),
			0x80|byte((r>>12)&0x3F),
			0x80|byte((r>>6)&0x3F), 0x80|byte(r&0x3F))
	}
}
