// Package u16 provides stateful byte-level UTF-16LE/BE decoding and encoding.
package u16

import "ontology/scalar"

// Event classifies a UTF-16 decoder result.
type Event int

const (
	Pending Event = iota
	Scalar
	Invalid
)

// Result is the outcome of feeding bytes.
type Result struct {
	Kind Event
	Rune rune
	// Size is the finished unit's consumed input byte count.
	Size int
}

// Decoder is a stateful UTF-16 byte decoder for one fixed byte order. Bytes
// are fed in stream order; odd/even position decides high/low byte.
type Decoder struct {
	hiByte byte
	half   bool // one byte of the next code unit is buffered
	cu     uint16
	high   bool // a high surrogate is waiting for its low surrogate
	big    bool
}

// NewDecoder creates a decoder for the given byte order.
func NewDecoder(big bool) *Decoder { return &Decoder{big: big} }

func (d *Decoder) Reset() { d.half, d.high, d.cu = false, false, 0 }

// PendingLen returns bytes held in an incomplete prefix (0..3).
func (d *Decoder) PendingLen() int {
	n := 0
	if d.half {
		n++
	}
	if d.high {
		n += 2
	}
	return n
}

// HighPending reports whether a lone high surrogate is buffered.
func (d *Decoder) HighPending() bool { return d.high && !d.half }

func (d *Decoder) unit(u uint16) Result {
	switch {
	case d.high && scalar.IsLowSurrogate(u):
		r := scalar.SurrogatePair(d.cu, u)
		d.high = false
		return Result{Kind: Scalar, Rune: r, Size: 4}
	case scalar.IsHighSurrogate(u):
		d.cu, d.high = u, true
		return Result{Kind: Pending}
	case scalar.IsLowSurrogate(u):
		return Result{Kind: Invalid, Size: 2}
	default:
		d.high = false
		return Result{Kind: Scalar, Rune: rune(u), Size: 2}
	}
}

// Feed supplies one byte in stream order.
func (d *Decoder) Feed(b byte) Result {
	if d.half {
		d.half = false
		var u uint16
		if d.big {
			u = uint16(d.hiByte)<<8 | uint16(b)
		} else {
			u = uint16(b)<<8 | uint16(d.hiByte)
		}
		return d.unit(u)
	}
	d.hiByte, d.half = b, true
	return Result{Kind: Pending}
}

// Drain closes the stream: an odd trailing byte or a lone high surrogate is a
// truncated prefix.
func (d *Decoder) Drain() (size int, ok bool) {
	n := d.PendingLen()
	if n == 0 {
		return 0, true
	}
	d.Reset()
	return n, false
}

// Encode appends the UTF-16 code units of scalar r to p. big selects order.
func Encode(p []byte, r rune, big bool) []byte {
	var units [2]uint16
	n := 1
	if r >= 0x10000 {
		units[0], units[1] = scalar.EncodeSurrogatePair(r)
		n = 2
	} else {
		units[0] = uint16(r)
	}
	for i := 0; i < n; i++ {
		if big {
			p = append(p, byte(units[i]>>8), byte(units[i]))
		} else {
			p = append(p, byte(units[i]), byte(units[i]>>8))
		}
	}
	return p
}
