// Package u16 decodes and encodes UTF-16 (little- or big-endian) units.
package u16

import "ontology/scalar"

// Order selects a UTF-16 byte order.
type Order int

// Byte orders.
const (
	LE Order = iota
	BE
)

// Unit is one decoded result.
type Unit struct {
	R     rune
	Bad   bool
	N     int
	Trunc bool
	NoBOM bool
}

// Decoder is an incremental UTF-16 decoder.
// Ord<0 means "decide order from a stream-initial BOM".
type Decoder struct {
	Ord      Order
	auto     bool
	sniff    []byte
	ordReady bool
	lo       byte
	haveLo   bool
	hi       rune
	checks   int64
}

// NewDecoder returns a decoder for ord; pass -1 to require an initial BOM.
func NewDecoder(ord Order) *Decoder {
	d := &Decoder{Ord: ord, hi: -1}
	if ord < 0 {
		d.auto = true
	}
	return d
}

// Checks reports byte examinations (excluding BOM sniff lookback).
func (d *Decoder) Checks() int64 { return d.checks }

// Pending reports buffered bytes (0 or 1).
func (d *Decoder) Pending() int {
	if d.auto {
		if len(d.sniff) < 2 {
			return len(d.sniff)
		}
		return 0
	}
	if d.haveLo {
		return 1
	}
	return 0
}

func (d *Decoder) cu(cu rune) []Unit {
	switch {
	case scalar.HighSurrogate(cu):
		if d.hi >= 0 {
			h := d.hi
			d.hi = cu
			return []Unit{{R: h, Bad: true, N: 2}}
		}
		d.hi = cu
	case scalar.LowSurrogate(cu):
		if d.hi >= 0 {
			r := scalar.CombineSurrogates(d.hi, cu)
			d.hi = -1
			return []Unit{{R: r, N: 4}}
		}
		return []Unit{{R: cu, Bad: true, N: 2}}
	default:
		if d.hi >= 0 {
			h := d.hi
			d.hi = -1
			return []Unit{{R: h, Bad: true, N: 2}, {R: cu, N: 2}}
		}
		return []Unit{{R: cu, N: 2}}
	}
	return nil
}

// Feed examines one byte. Before the order is known it buffers for the BOM;
// once the BOM arrives it replays the stream from the byte after the BOM.
func (d *Decoder) Feed(b byte) []Unit {
	if d.auto && !d.ordReady {
		d.sniff = append(d.sniff, b)
		if len(d.sniff) < 2 {
			return nil
		}
		switch {
		case d.sniff[0] == 0xFF && d.sniff[1] == 0xFE:
			d.Ord, d.ordReady = LE, true
		case d.sniff[0] == 0xFE && d.sniff[1] == 0xFF:
			d.Ord, d.ordReady = BE, true
		default:
			d.ordReady = true
			d.auto = false
			return []Unit{{R: -1, Bad: true, N: len(d.sniff), NoBOM: true}}
		}
		rest := d.sniff[2:]
		d.sniff = nil
		var out []Unit
		for _, x := range rest {
			out = append(out, d.Feed(x)...)
		}
		return out
	}
	d.checks++
	if !d.haveLo {
		d.lo, d.haveLo = b, true
		return nil
	}
	d.haveLo = false
	var cu rune
	if d.Ord == LE {
		cu = rune(d.lo) | rune(b)<<8
	} else {
		cu = rune(b) | rune(d.lo)<<8
	}
	return d.cu(cu)
}

// Finish flushes an odd byte (truncation) and an orphan high surrogate.
func (d *Decoder) Finish() []Unit {
	var out []Unit
	if d.auto && !d.ordReady && len(d.sniff) > 0 {
		n := len(d.sniff)
		d.sniff = nil
		return []Unit{{R: -1, Bad: true, N: n, Trunc: true}}
	}
	if d.haveLo {
		d.haveLo = false
		out = append(out, Unit{R: -1, Bad: true, N: 1, Trunc: true})
	}
	if d.hi >= 0 {
		out = append(out, Unit{R: d.hi, Bad: true, N: 2, Trunc: true})
		d.hi = -1
	}
	return out
}

// EncodeLen is the encoded length of scalar r.
func EncodeLen(r rune) int {
	if r >= 0x10000 {
		return 4
	}
	return 2
}

// Encode appends scalar r (or a surrogate pair) in ord to dst.
func Encode(dst []byte, r rune, ord Order) []byte {
	one := func(cu uint16) []byte {
		if ord == LE {
			return append(dst, byte(cu), byte(cu>>8))
		}
		return append(dst, byte(cu>>8), byte(cu))
	}
	if r >= 0x10000 {
		v := r - 0x10000
		dst = one(0xD800 + uint16(v>>10))
		return one(0xDC00 + uint16(v&0x3FF))
	}
	return one(uint16(r))
}
