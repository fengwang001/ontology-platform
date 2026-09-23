// Package u8 decodes and encodes UTF-8 at byte level, without unicode/utf8.
package u8

// BOM is the UTF-8 encoding of U+FEFF.
var BOM = [3]byte{0xEF, 0xBB, 0xBF}

// Event class returned by Decoder.Feed.
const (
	Need     = 0 // prefix buffered, feed more bytes
	Scalar   = 1 // Rune is a legal scalar, Len bytes finalized
	Illegal  = 2 // one illegal unit of Len bytes finalized
	Retry    = 3 // Len buffered bytes finalized as illegal; re-feed b
)

// Decoder is a single-attempt, byte-at-a-time UTF-8 decoder.
type Decoder struct {
	buf    [3]byte
	need   int
	got    int
	min2   byte
	max2   byte
	Checks int64
}

// Event is one Feed result.
type Event struct {
	Kind int
	Rune rune
	Len  int
}

func isCont(b byte) bool { return b >= 0x80 && b <= 0xBF }

// Feed examines one byte. Every examined byte increments Checks; a byte that
// fails a continuation is re-fed, so any byte is examined at most twice.
func (d *Decoder) Feed(b byte) Event {
	d.Checks++
	if d.need == 0 {
		switch {
		case b < 0x80:
			return Event{Kind: Scalar, Rune: rune(b), Len: 1}
		case b >= 0xC2 && b <= 0xDF:
			d.need, d.got, d.buf[0], d.min2, d.max2 = 2, 1, b, 0x80, 0xBF
		case b == 0xE0:
			d.need, d.got, d.buf[0], d.min2, d.max2 = 3, 1, b, 0xA0, 0xBF
		case b >= 0xE1 && b <= 0xEC:
			d.need, d.got, d.buf[0], d.min2, d.max2 = 3, 1, b, 0x80, 0xBF
		case b == 0xED:
			d.need, d.got, d.buf[0], d.min2, d.max2 = 3, 1, b, 0x80, 0x9F
		case b >= 0xEE && b <= 0xEF:
			d.need, d.got, d.buf[0], d.min2, d.max2 = 3, 1, b, 0x80, 0xBF
		case b == 0xF0:
			d.need, d.got, d.buf[0], d.min2, d.max2 = 4, 1, b, 0x90, 0xBF
		case b >= 0xF1 && b <= 0xF3:
			d.need, d.got, d.buf[0], d.min2, d.max2 = 4, 1, b, 0x80, 0xBF
		case b == 0xF4:
			d.need, d.got, d.buf[0], d.min2, d.max2 = 4, 1, b, 0x80, 0x8F
		default: // 80..BF, C0, C1, F5..FF
			return Event{Kind: Illegal, Len: 1}
		}
		return Event{Kind: Need}
	}
	lo, hi := byte(0x80), byte(0xBF)
	if d.got == 1 {
		lo, hi = d.min2, d.max2
	}
	if b < lo || b > hi {
		n := d.got
		d.need = 0
		return Event{Kind: Retry, Len: n}
	}
	d.buf[d.got-1] = b
	d.got++
	if d.got < d.need {
		return Event{Kind: Need}
	}
	r := decode(d.buf[:d.need], d.need)
	d.need = 0
	return Event{Kind: Scalar, Rune: r, Len: d.need}
}

func decode(p []byte, n int) rune {
	switch n {
	case 2:
		return rune(p[0]&0x1F)<<6 | rune(p[1]&0x3F)
	case 3:
		return rune(p[0]&0x0F)<<12 | rune(p[1]&0x3F)<<6 | rune(p[2]&0x3F)
	default:
		return rune(p[0]&0x07)<<18 | rune(p[1]&0x3F)<<12 |
			rune(p[2]&0x3F)<<6 | rune(p[3]&0x3F)
	}
}

// Pending reports a buffered, still-valid prefix length (0 at boundary).
func (d *Decoder) Pending() int { return d.got }

// Drop discards a buffered prefix; returns its length. Used at Close.
func (d *Decoder) Drop() int {
	n := d.got
	d.need, d.got = 0, 0
	return n
}

// IsCont reports whether b is a continuation byte 80..BF.
func IsCont(b byte) bool { return isCont(b) }

// ValidPrefix reports whether lead followed by cont is a still-valid but
// incomplete attempt (all seen bytes inside their allowed windows).
func ValidPrefix(lead byte, cont []byte) bool {
	var d Decoder
	if d.Feed(lead).Kind != Need {
		return false
	}
	for _, b := range cont {
		e := d.Feed(b)
		if e.Kind != Need {
			return false
		}
	}
	return true
}

// LeadLen returns the required length for a lead byte, or 0 if illegal.
func LeadLen(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b >= 0xC2 && b <= 0xDF:
		return 2
	case b >= 0xE0 && b <= 0xEF:
		return 3
	case b >= 0xF0 && b <= 0xF4:
		return 4
	}
	return 0
}

// Encode appends the UTF-8 encoding of scalar r to dst.
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
