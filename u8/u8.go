// Package u8 decodes and encodes UTF-8 byte sequences scalar by scalar,
// without using unicode/utf8 or implicit string decoding.
package u8

import "ontology/scalar"

// Push outcomes.
const (
	NeedMore      = 0 // b extends a valid (possibly incomplete) prefix
	RuneReady     = 1 // a complete valid scalar is available
	InvalidByte   = 2 // b itself is one invalid unit (already consumed)
	InvalidPrefix = 3 // buffered prefix is one invalid unit; b is NOT consumed
)

// Decoder is a byte-at-a-time UTF-8 decoder. It is not safe for
// concurrent use.
type Decoder struct {
	pref [3]byte
	plen int
	want int // total expected length
	val  rune
	lo   byte
	hi   byte
}

func (d *Decoder) Reset() { d.plen = 0 }

// PendingLen is the number of buffered valid-prefix bytes (0..3).
func (d *Decoder) PendingLen() int { return d.plen }

// Pending returns the buffered valid-prefix bytes.
func (d *Decoder) Pending() []byte { return d.pref[:d.plen] }

func cont(b byte) bool { return b >= 0x80 && b <= 0xBF }

// Push feeds one byte. See outcome constants. On RuneReady the value
// is available via Rune.
func (d *Decoder) Push(b byte) int {
	if d.plen == 0 {
		switch {
		case b < 0x80:
			d.val = rune(b)
			return RuneReady
		case cont(b), b == 0xC0, b == 0xC1, b >= 0xF5:
			return InvalidByte
		}
		d.pref[0] = b
		d.plen, d.want = 1, 0
		d.lo, d.hi = 0x80, 0xBF
		d.val = rune(b & 0x1F)
		switch {
		case b <= 0xDF:
			d.want = 2
		case b == 0xE0:
			d.want, d.lo = 3, 0xA0
		case b <= 0xEC:
			d.want = 3
		case b == 0xED:
			d.want, d.hi = 3, 0x9F
		case b <= 0xEF:
			d.want = 3
		case b == 0xF0:
			d.want, d.lo, d.val = 4, 0x90, rune(b&0x07)
		case b <= 0xF3:
			d.want, d.val = 4, rune(b&0x07)
		default: // F4
			d.want, d.hi, d.val = 4, 0x8F, rune(b&0x07)
		}
		return NeedMore
	}
	ok := cont(b)
	if d.plen == 1 {
		ok = ok && b >= d.lo && b <= d.hi
	}
	if !ok {
		d.plen = 0
		return InvalidPrefix
	}
	d.pref[d.plen] = b
	d.plen++
	d.val = d.val<<6 | rune(b&0x3F)
	if d.plen == d.want {
		r := d.val
		d.plen = 0
		if scalar.Valid(r) {
			d.val = r
			return RuneReady
		}
		return InvalidByte // unreachable: ranges already excluded
	}
	return NeedMore
}

// Rune returns the last decoded valid scalar.
func (d *Decoder) Rune() rune { return d.val }

// Len returns the UTF-8 byte length of scalar r (must be valid).
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

// Encode appends the UTF-8 encoding of r to dst.
func Encode(dst []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r)&0x3F)
	case r < 0x10000:
		return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	default:
		return append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12)&0x3F,
			0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	}
}
