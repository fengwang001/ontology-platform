// Package u8 decodes and encodes UTF-8 byte sequences without relying on
// the unicode/utf8 package.
package u8

import "ontology/scalar"

// Kind classifies one decoder event.
type Kind int

const (
	Rune      Kind = iota // decoded scalar value
	Invalid               // one illegal unit
	Incomplete            // unfinished legal-looking prefix at end of stream
	NeedMore              // more bytes required
)

// Event is one Feed result. N is the number of raw bytes in the resolved
// unit; Again holds bytes that were rejected and must be reprocessed.
type Event struct {
	Kind  Kind
	Rune  rune
	N     int
	Again []byte
}

// Decoder is a stateful, incremental UTF-8 decoder (no unicode/utf8).
type Decoder struct {
	buf  [3]byte
	n    int
	need int
}

func NewDecoder() *Decoder { return &Decoder{} }

func isCont(b byte) bool { return b&0xC0 == 0x80 }

// secondOK reports whether b is a legal second byte after lead c.
func secondOK(c, b byte) bool {
	if !isCont(b) {
		return false
	}
	switch {
	case c == 0xE0:
		return b >= 0xA0
	case c == 0xED:
		return b <= 0x9F
	case c == 0xF0:
		return b >= 0x90
	case c == 0xF4:
		return b <= 0x8F
	default:
		return true
	}
}

// Feed supplies one byte and returns one event.
func (d *Decoder) Feed(b byte) Event {
	if d.n == 0 {
		switch {
		case b < 0x80:
			return Event{Kind: Rune, Rune: rune(b), N: 1}
		case b < 0xC2: // 80..BF stray continuation, C0 C1 non-shortest
			return Event{Kind: Invalid, N: 1}
		case b < 0xE0:
			d.need = 2
		case b < 0xF0:
			d.need = 3
		case b < 0xF5:
			d.need = 4
		default: // F5..FF
			return Event{Kind: Invalid, N: 1}
		}
		d.buf[0] = b
		d.n = 1
		return Event{Kind: NeedMore}
	}
	pos := d.n
	if pos == 1 && !secondOK(d.buf[0], b) {
		d.n, d.need = 0, 0
		return Event{Kind: Invalid, N: 1, Again: []byte{b}}
	}
	if pos > 1 && !isCont(b) {
		n := d.n
		d.n, d.need = 0, 0
		return Event{Kind: Invalid, N: n, Again: []byte{b}}
	}
	d.buf[pos] = b
	d.n++
	if d.n < d.need {
		return Event{Kind: NeedMore}
	}
	r := decode(d.buf[:d.need])
	n := d.need
	d.n, d.need = 0, 0
	return Event{Kind: Rune, Rune: r, N: n}
}

func decode(p []byte) rune {
	var r rune
	switch len(p) {
	case 2:
		r = rune(p[0]&0x1F)<<6 | rune(p[1]&0x3F)
	case 3:
		r = rune(p[0]&0x0F)<<12 | rune(p[1]&0x3F)<<6 | rune(p[2]&0x3F)
	default:
		r = rune(p[0]&0x07)<<18 | rune(p[1]&0x3F)<<12 | rune(p[2]&0x3F)<<6 | rune(p[3]&0x3F)
	}
	return r
}

// Len returns the count of buffered bytes (0..3).
func (d *Decoder) Len() int { return d.n }

// Reset clears buffered state.
func (d *Decoder) Reset() { d.n, d.need = 0, 0 }

// Flush resolves buffered bytes at end of stream.
func (d *Decoder) Flush() Event {
	if d.n == 0 {
		return Event{Kind: NeedMore}
	}
	n := d.n
	d.n, d.need = 0, 0
	return Event{Kind: Incomplete, N: n}
}

// EncodeLen is the UTF-8 length of scalar r.
func EncodeLen(r rune) int {
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
	if !scalar.Valid(r) {
		r = 0xFFFD
	}
	switch EncodeLen(r) {
	case 1:
		return append(dst, byte(r))
	case 2:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r)&0x3F)
	case 3:
		return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	default:
		return append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12)&0x3F, 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	}
}
