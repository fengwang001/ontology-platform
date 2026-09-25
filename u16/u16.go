// Package u16 decodes and encodes UTF-16LE/UTF-16BE, including surrogate
// pairs and lone surrogates. All byte-level checks are hand-written.
package u16

const (
	highLo = 0xD800
	highHi = 0xDBFF
	lowLo  = 0xDC00
	lowHi  = 0xDFFF
)

// IsHigh reports whether w is a high surrogate.
func IsHigh(w uint16) bool { return w >= highLo && w <= highHi }

// IsLow reports whether w is a low surrogate.
func IsLow(w uint16) bool { return w >= lowLo && w <= lowHi }

// Combine joins a high and a low surrogate into a scalar value.
func Combine(hi, lo uint16) rune {
	return 0x10000 + (rune(hi-highLo) << 10) + rune(lo-lowLo)
}

// Split returns the high and low surrogate for an astral scalar value.
func Split(cp rune) (hi, lo uint16) {
	v := cp - 0x10000
	return highLo + uint16(v>>10), lowLo + uint16(v)&0x3FF
}

// Encode writes the UTF-16 encoding of cp (1 or 2 code units) into dst
// (len >= 4) in the given byte order and returns the bytes written.
func Encode(dst []byte, cp rune, be bool) int {
	put := func(w uint16, i int) {
		if be {
			dst[i], dst[i+1] = byte(w>>8), byte(w)
		} else {
			dst[i], dst[i+1] = byte(w), byte(w>>8)
		}
	}
	if cp < 0x10000 {
		put(uint16(cp), 0)
		return 2
	}
	hi, lo := Split(cp)
	put(hi, 0)
	put(lo, 2)
	return 4
}

// Stepper is an incremental UTF-16 decoder: one pass, no backtracking.
type Stepper struct {
	odd   int  // pending odd byte value, -1 = none
	his   int  // pending high surrogate, -1 = none
	be    bool // current byte order
	first bool // BOM detection still pending
}

// NewStepper returns a Stepper in the given byte order. If bom is true, a
// leading BOM is recognized (FEFF keeps the order, FFFE swaps it) and
// reported as a scalar U+FEFF for the caller to drop.
func NewStepper(be, bom bool) Stepper {
	return Stepper{odd: -1, his: -1, be: be, first: bom}
}

// Pending returns the buffered bytes of the incomplete tail (<= 3).
func (s *Stepper) Pending() int {
	n := 0
	if s.odd >= 0 {
		n++
	}
	if s.his >= 0 {
		n += 2
	}
	return n
}

// Step feeds one byte. emit is invoked for each completed unit as
// (cp, size, valid); a lone surrogate is an invalid unit of 2 bytes
// reported with cp == 0. A non-low unit following a high surrogate is
// reprocessed, not swallowed. If emit returns false Step stops early.
func (s *Stepper) Step(b byte, emit func(cp rune, size int, valid bool) bool) {
	if s.odd < 0 {
		s.odd = int(b)
		return
	}
	w := uint16(s.odd) | uint16(b)<<8
	if s.be {
		w = uint16(s.odd)<<8 | uint16(b)
	}
	s.odd = -1
	if s.first {
		s.first = false
		if w == 0xFFFE {
			s.be = !s.be
			w = 0xFEFF
		}
		if w == 0xFEFF {
			emit(0xFEFF, 2, true)
			return
		}
	}
	for {
		switch {
		case IsHigh(w):
			if s.his >= 0 && !emit(0, 2, false) {
				return
			}
			s.his = int(w)
			return
		case IsLow(w):
			if s.his < 0 {
				emit(0, 2, false)
				return
			}
			cp := Combine(uint16(s.his), w)
			s.his = -1
			emit(cp, 4, true)
			return
		default:
			if s.his >= 0 {
				s.his = -1
				if !emit(0, 2, false) {
					return
				}
				continue // reprocess w as a fresh unit
			}
			emit(rune(w), 2, true)
			return
		}
	}
}

// Flush reports a truncated tail (lone high surrogate, then odd byte) at
// end of stream as invalid units.
func (s *Stepper) Flush(emit func(cp rune, size int, valid bool) bool) {
	if s.his >= 0 {
		s.his = -1
		if !emit(0, 2, false) {
			return
		}
	}
	if s.odd >= 0 {
		s.odd = -1
		emit(0, 1, false)
	}
}
