// Package u8 decodes and encodes UTF-8 one scalar at a time, byte by byte,
// without using unicode/utf8. Invalid input is split into maximal-subpart
// units (see DESIGN.md).
package u8

type Event int

const (
	None    Event = iota // unit incomplete, need more bytes
	Scalar               // decoded a valid scalar
	Invalid              // the pending prefix is one invalid unit
)

// Stepper is an incremental UTF-8 unit decoder. Each byte is examined once.
type Stepper struct {
	need int  // continuation bytes still expected
	lo   byte // allowed range for the next continuation byte
	hi   byte
	acc  rune
	N    int // bytes held in the current unresolved unit (<= 3)
}

func (s *Stepper) Reset() { *s = Stepper{} }

// Feed offers b to the current unit. consumed is false only when b rejects a
// pending prefix: the prefix is then Invalid and b must be fed again as the
// start of the next unit.
func (s *Stepper) Feed(b byte) (ev Event, sc rune, consumed bool) {
	if s.need == 0 {
		s.N = 1
		switch {
		case b < 0x80:
			return Scalar, rune(b), true
		case b >= 0xC2 && b <= 0xDF:
			s.need, s.lo, s.hi, s.acc = 1, 0x80, 0xBF, rune(b&0x1F)
		case b == 0xE0:
			s.need, s.lo, s.hi, s.acc = 2, 0xA0, 0xBF, rune(b&0x0F)
		case b >= 0xE1 && b <= 0xEC || b == 0xEE || b == 0xEF:
			s.need, s.lo, s.hi, s.acc = 2, 0x80, 0xBF, rune(b&0x0F)
		case b == 0xED:
			s.need, s.lo, s.hi, s.acc = 2, 0x80, 0x9F, rune(b&0x0F)
		case b == 0xF0:
			s.need, s.lo, s.hi, s.acc = 3, 0x90, 0xBF, rune(b&0x07)
		case b >= 0xF1 && b <= 0xF3:
			s.need, s.lo, s.hi, s.acc = 3, 0x80, 0xBF, rune(b&0x07)
		case b == 0xF4:
			s.need, s.lo, s.hi, s.acc = 3, 0x80, 0x8F, rune(b&0x07)
		default: // 80..BF, C0, C1, F5..FF: invalid lead, unit = 1 byte
			return Invalid, 0, true
		}
		return None, 0, true
	}
	if b < s.lo || b > s.hi { // b rejects the pending prefix; do not consume
		return Invalid, 0, false
	}
	s.N++
	s.acc = s.acc<<6 | rune(b&0x3F)
	s.need--
	s.lo, s.hi = 0x80, 0xBF
	if s.need == 0 {
		return Scalar, s.acc, true
	}
	return None, 0, true
}

// Decode parses one unit from p and returns its length n and, for a valid
// unit, the scalar. n == 0 means p holds only an incomplete valid prefix.
func Decode(p []byte) (n int, sc rune, ok bool) {
	var s Stepper
	for i := 0; i < len(p); i++ {
		ev, v, _ := s.Feed(p[i])
		if ev == Scalar {
			return i + 1, v, true
		}
		if ev == Invalid {
			return s.N, 0, false
		}
	}
	return 0, 0, false
}

// Len returns the encoded byte length of a valid scalar.
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

// Encode appends the UTF-8 encoding of a valid scalar to dst.
func Encode(dst []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r)&0x3F)
	case r < 0x10000:
		return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	default:
		return append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12)&0x3F, 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	}
}
