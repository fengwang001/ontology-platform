// Package u8 implements byte-level UTF-8 scalar decoding and encoding.
package u8

import "ontology/scalar"

// Unit describes one resolved scalar or one illegal unit.
type Unit struct {
	R     scalar.Rune
	Bad   bool
	Start int64 // global byte offset of the unit's first byte
	Size  int   // bytes swallowed by the unit
}

// IsCont reports whether b is a 10xxxxxx continuation byte.
func IsCont(b byte) bool { return b&0xC0 == 0x80 }

// LeadLen returns the expected sequence length of lead byte b.
// Continuation bytes and C0 C1 F5..FF yield 1 (self-contained illegal unit).
func LeadLen(b byte) int {
	switch {
	case b >= 0xC2 && b <= 0xDF:
		return 2
	case b >= 0xE0 && b <= 0xEF:
		return 3
	case b >= 0xF0 && b <= 0xF4:
		return 4
	default:
		return 1
	}
}

func secondOK(first, second byte) bool {
	switch first {
	case 0xE0:
		return second >= 0xA0
	case 0xED:
		return second <= 0x9F
	case 0xF0:
		return second >= 0x90
	case 0xF4:
		return second <= 0x8F
	}
	return true
}

// Decoder is a resumable UTF-8 byte state machine. Prefix is at most 3 bytes.
type Decoder struct {
	pref  [3]byte
	n     int
	start int64
}

// MaxPrefix is the hard upper bound on buffered bytes at any time.
const MaxPrefix = 3

// Pending returns the currently buffered (unresolved) bytes.
func (d *Decoder) Pending() int { return d.n }

// Feed resolves units from p, invoking emit exactly once per unit. base is the
// global offset of p[0]. It returns the number of bytes of p consumed; bytes
// retained for an unfinished prefix are not counted.
func (d *Decoder) Feed(p []byte, base int64, emit func(Unit)) int {
	i := 0
	for i < len(p) {
		if d.n == 0 {
			d.pref[0] = p[i]
			d.n = 1
			d.start = base + int64(i)
			i++
		}
		lead := d.pref[0]
		k := LeadLen(lead)
		for d.n < k && i < len(p) {
			b := p[i]
			if !IsCont(b) || (d.n == 1 && !secondOK(lead, b)) {
				break
			}
			d.pref[d.n] = b
			d.n++
			i++
		}
		if d.n < k && i < len(p) {
			emit(Unit{R: scalar.Replacement, Bad: true, Start: d.start, Size: d.n})
			d.n = 0
			continue // p[i] rejected as continuation: restart on it
		}
		if d.n < k {
			return i // EOF inside prefix: retain
		}
		emit(Unit{R: decode(lead, d.pref[1:]), Start: d.start, Size: k})
		d.n = 0
	}
	return i
}

func decode(lead byte, rest []byte) scalar.Rune {
	switch len(rest) + 1 {
	case 1:
		return scalar.Rune(lead)
	case 2:
		return scalar.Rune(lead&0x1F)<<6 | scalar.Rune(rest[0]&0x3F)
	case 3:
		return scalar.Rune(lead&0x0F)<<12 | scalar.Rune(rest[0]&0x3F)<<6 |
			scalar.Rune(rest[1]&0x3F)
	}
	return scalar.Rune(lead&0x07)<<18 | scalar.Rune(rest[0]&0x3F)<<12 |
		scalar.Rune(rest[1]&0x3F)<<6 | scalar.Rune(rest[2]&0x3F)
}

// Finish resolves a prefix left over at end of stream.
func (d *Decoder) Finish() (Unit, bool) {
	if d.n == 0 {
		return Unit{}, false
	}
	u := Unit{R: scalar.Replacement, Bad: true, Start: d.start, Size: d.n}
	d.n = 0
	return u, true
}

// Encode appends the UTF-8 encoding of scalar r to dst.
func Encode(dst []byte, r scalar.Rune) []byte {
	switch {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
	case r < 0x10000:
		return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6&0x3F),
			0x80|byte(r&0x3F))
	}
	return append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12&0x3F),
		0x80|byte(r>>6&0x3F), 0x80|byte(r&0x3F))
}
