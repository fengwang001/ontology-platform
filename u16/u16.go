// Package u16 decodes and encodes UTF-16 (LE and BE) one code unit at a
// time, without using unicode/utf16. Lone surrogates are invalid units of
// 2 bytes; a high surrogate followed by a non-low unit leaves that unit to
// be reprocessed on its own.
package u16

import "ontology/scalar"

type Event int

const (
	None    Event = iota // waiting for more units (high surrogate held)
	Scalar               // decoded a valid scalar
	Invalid              // the held high surrogate is a lone unit (2 bytes)
)

// Stepper is an incremental UTF-16 decoder fed with whole code units.
type Stepper struct {
	hi    uint16 // held high surrogate
	has   bool
	stash uint16 // unit to reprocess after an Invalid event
	more  bool
	N     int // pending raw bytes: 2 while a high surrogate is held, else 0
}

func (s *Stepper) Reset() { *s = Stepper{} }

// Feed16 consumes one code unit. n is the number of input bytes the event
// resolves (2 or 4). If more is true, call Again for the next event.
func (s *Stepper) Feed16(w uint16) (ev Event, sc rune, n int, more bool) {
	if !s.has {
		return s.start(w)
	}
	s.has, s.N = false, 0
	if scalar.IsLow(w) {
		return Scalar, scalar.Combine(s.hi, w), 4, false
	}
	s.stash, s.more = w, true // reprocess w as a fresh unit
	return Invalid, 0, 2, true
}

// Again delivers the deferred event for the stashed unit.
func (s *Stepper) Again() (ev Event, sc rune, n int, more bool) {
	s.more = false
	return s.start(s.stash)
}

func (s *Stepper) start(w uint16) (Event, rune, int, bool) {
	switch {
	case scalar.IsHigh(w):
		s.hi, s.has, s.N = w, true, 2
		return None, 0, 0, false
	case scalar.IsLow(w):
		return Invalid, 0, 2, false
	default:
		return Scalar, rune(w), 2, false
	}
}

// Encode appends the UTF-16 encoding of a valid scalar (surrogate pair for
// r >= 0x10000) to dst, in big- or little-endian byte order.
func Encode(dst []byte, r rune, be bool) []byte {
	put := func(w uint16) []byte {
		if be {
			return append(dst, byte(w>>8), byte(w))
		}
		return append(dst, byte(w), byte(w>>8))
	}
	if r < 0x10000 {
		return put(uint16(r))
	}
	r -= 0x10000
	dst = put(scalar.HiFirst + uint16(r>>10))
	return put(scalar.LoFirst + uint16(r&0x3FF))
}
