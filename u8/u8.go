// Package u8 decodes and encodes UTF-8 byte sequences one scalar at a
// time, reporting how many bytes each unit consumes and whether it is a
// legal scalar or an invalid unit. All byte-level checks are hand-written.
package u8

// Lead classifies a lead byte: total sequence length and the valid range
// [min, max] for the second byte (later bytes must be in 80..BF). ok is
// false when b can never start a sequence (80..C1, F5..FF).
func Lead(b byte) (length int, min, max byte, ok bool) {
	switch {
	case b < 0x80:
		return 1, 0, 0, true
	case b < 0xC2:
		return 0, 0, 0, false
	case b < 0xE0:
		return 2, 0x80, 0xBF, true
	case b == 0xE0:
		return 3, 0xA0, 0xBF, true
	case b < 0xED:
		return 3, 0x80, 0xBF, true
	case b == 0xED:
		return 3, 0x80, 0x9F, true
	case b < 0xF0:
		return 3, 0x80, 0xBF, true
	case b == 0xF0:
		return 4, 0x90, 0xBF, true
	case b < 0xF4:
		return 4, 0x80, 0xBF, true
	case b == 0xF4:
		return 4, 0x80, 0x8F, true
	default:
		return 0, 0, 0, false
	}
}

// Decode decodes the first unit in b (non-empty): (cp, size, true) for a
// legal scalar, (0, size, false) for an invalid unit swallowing size >= 1
// bytes (maximal valid prefix), (0, 0, false) for an incomplete prefix.
func Decode(b []byte) (cp rune, size int, ok bool) {
	length, min, max, lead := Lead(b[0])
	if !lead {
		return 0, 1, false
	}
	if length == 1 {
		return rune(b[0]), 1, true
	}
	if len(b) < 2 {
		return 0, 0, false
	}
	if b[1] < min || b[1] > max {
		return 0, 1, false
	}
	cp = rune(b[0])&(0x7F>>uint(length))<<6 | rune(b[1]&0x3F)
	for i := 2; i < length; i++ {
		if len(b) <= i {
			return 0, 0, false
		}
		if b[i] < 0x80 || b[i] > 0xBF {
			return 0, i, false
		}
		cp = cp<<6 | rune(b[i]&0x3F)
	}
	return cp, length, true
}

// Encode writes the UTF-8 encoding of cp into dst (len >= 4) and returns
// the number of bytes written. cp must be a valid scalar value.
func Encode(dst []byte, cp rune) int {
	switch {
	case cp < 0x80:
		dst[0] = byte(cp)
		return 1
	case cp < 0x800:
		dst[0] = 0xC0 | byte(cp>>6)
		dst[1] = 0x80 | byte(cp)&0x3F
		return 2
	case cp < 0x10000:
		dst[0] = 0xE0 | byte(cp>>12)
		dst[1] = 0x80 | byte(cp>>6)&0x3F
		dst[2] = 0x80 | byte(cp)&0x3F
		return 3
	default:
		dst[0] = 0xF0 | byte(cp>>18)
		dst[1] = 0x80 | byte(cp>>12)&0x3F
		dst[2] = 0x80 | byte(cp>>6)&0x3F
		dst[3] = 0x80 | byte(cp)&0x3F
		return 4
	}
}

// Stepper is an incremental UTF-8 decoder: one pass, no backtracking.
// The zero value is ready to use.
type Stepper struct {
	need   int
	lo, hi byte
	cp     rune
	pend   int
}

// Pending returns the buffered bytes of the current incomplete unit (<= 3).
func (s *Stepper) Pending() int { return s.pend }

// Step feeds one byte. emit is invoked for each completed unit as
// (cp, size, valid); an invalid unit is reported with cp == 0 and the
// number of swallowed bytes. If emit returns false Step stops early; the
// caller is expected to go terminal in that case.
func (s *Stepper) Step(b byte, emit func(cp rune, size int, valid bool) bool) {
	for {
		if s.need == 0 {
			if b < 0x80 {
				emit(rune(b), 1, true)
				return
			}
			length, lo, hi, ok := Lead(b)
			if !ok {
				emit(0, 1, false)
				return
			}
			s.need, s.lo, s.hi, s.pend = length-1, lo, hi, 1
			s.cp = rune(b) & (0x7F >> uint(length))
			return
		}
		if b >= s.lo && b <= s.hi {
			s.cp = s.cp<<6 | rune(b&0x3F)
			s.lo, s.hi = 0x80, 0xBF
			s.need--
			s.pend++
			if s.need == 0 {
				cp, n := s.cp, s.pend
				s.pend = 0
				emit(cp, n, true)
			}
			return
		}
		n := s.pend // maximal valid prefix is one invalid unit; b is retried
		s.need, s.pend = 0, 0
		if !emit(0, n, false) {
			return
		}
	}
}

// Flush reports a truncated prefix at end of stream as one invalid unit.
func (s *Stepper) Flush(emit func(cp rune, size int, valid bool) bool) {
	if s.pend > 0 {
		n := s.pend
		s.pend, s.need = 0, 0
		emit(0, n, false)
	}
}
