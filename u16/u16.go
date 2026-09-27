// Package u16 implements byte-level UTF-16LE/UTF-16BE decoding and encoding.
package u16

import "ontology/scalar"

// Endian selects UTF-16 byte order; Auto detects it from a leading BOM.
type Endian int

const (
	Auto Endian = iota
	LE
	BE
)

// Unit is one resolved scalar or one illegal UTF-16 unit.
type Unit struct {
	R     scalar.Rune
	Bad   bool
	Start int64 // global byte offset of the unit's first byte
	Size  int   // bytes swallowed (2, or 4 for a pair)
	Trunc bool  // residue at end of stream
}

// MaxPrefix is the hard upper bound on buffered bytes at any time.
const MaxPrefix = 3

// Decoder is a resumable UTF-16 byte state machine. It buffers at most 3
// bytes: a high surrogate (2 bytes) plus at most one byte of the next unit.
type Decoder struct {
	end     Endian
	decided bool
	hi      scalar.Rune
	hiStart int64
	odd     byte
	hasOdd  bool
	oddSt   int64
}

// NewDecoder creates a decoder. With Auto, a leading FEFF/FFFE decides order;
// if neither appears, little-endian is assumed.
func NewDecoder(e Endian) *Decoder { return &Decoder{end: e} }

// Pending returns the count of buffered bytes (0..3).
func (d *Decoder) Pending() int {
	n := 0
	if d.hi != 0 {
		n += 2
	}
	if d.hasOdd {
		n++
	}
	return n
}

func (d *Decoder) pair(b0, b1 byte) scalar.Rune {
	if d.end == BE {
		return scalar.Rune(b0)<<8 | scalar.Rune(b1)
	}
	return scalar.Rune(b1)<<8 | scalar.Rune(b0)
}

// Feed resolves units from p with global base offset. Returns bytes consumed;
// bytes kept for an unfinished code unit or pending pair are not counted.
func (d *Decoder) Feed(p []byte, base int64, emit func(Unit)) int {
	i := 0
	if d.hasOdd && len(p) > 0 {
		d.hasOdd = false
		d.flush(d.odd, p[0], d.oddSt, emit)
		i = 1
	}
	for i+1 < len(p) {
		d.flush(p[i], p[i+1], base+int64(i), emit)
		i += 2
	}
	if i < len(p) {
		d.odd, d.hasOdd, d.oddSt = p[i], true, base+int64(i)
		i++
	}
	return i
}

func (d *Decoder) flush(b0, b1 byte, start int64, emit func(Unit)) {
	u := d.pair(b0, b1)
	if !d.decided && d.end == Auto {
		d.decided = true
		switch u {
		case 0xFEFF: // BOM: order stays LE, consume silently
			return
		case 0xFFFE: // impossible as LE text: switch to BE, consume
			d.end = BE
			return
		default:
			d.end = LE
		}
	}
	if d.hi != 0 {
		hi, hs := d.hi, d.hiStart
		d.hi = 0
		if scalar.IsLowSurrogate(u) {
			emit(Unit{R: scalar.JoinSurrogates(hi, u), Start: hs, Size: 4})
			return
		}
		emit(Unit{R: scalar.Replacement, Bad: true, Start: hs, Size: 2})
	}
	switch {
	case scalar.IsHighSurrogate(u):
		d.hi, d.hiStart = u, start
	case scalar.IsLowSurrogate(u):
		emit(Unit{R: scalar.Replacement, Bad: true, Start: start, Size: 2})
	default:
		emit(Unit{R: u, Start: start, Size: 2})
	}
}

// Finish handles end-of-stream residue: a lone high surrogate or a dangling
// odd byte is reported as truncated.
func (d *Decoder) Finish() (Unit, bool) {
	switch {
	case d.hi != 0:
		st := d.hiStart
		d.hi = 0
		return Unit{R: scalar.Replacement, Bad: true, Trunc: true,
			Start: st, Size: 2}, true
	case d.hasOdd:
		st := d.oddSt
		d.hasOdd = false
		return Unit{R: scalar.Replacement, Bad: true, Trunc: true,
			Start: st, Size: 1}, true
	}
	return Unit{}, false
}

// Encode appends the UTF-16 encoding of r to dst in the given byte order.
func Encode(dst []byte, e Endian, r scalar.Rune) []byte {
	put := func(u uint16) {
		if e == BE {
			dst = append(dst, byte(u>>8), byte(u))
	} else {
			dst = append(dst, byte(u), byte(u>>8))
		}
}
	if r >= 0x10000 {
		hi, lo := scalar.SplitSurrogate(r)
		put(uint16(hi))
		put(uint16(lo))
		return dst
	}
	put(uint16(r))
	return dst
}
