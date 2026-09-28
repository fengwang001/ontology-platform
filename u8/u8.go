// Package u8 provides stateful byte-level UTF-8 decoding and encoding.
package u8

import "ontology/scalar"

// Event classifies a decoder result.
type Event int

const (
	// Pending: b was buffered as part of an incomplete valid prefix.
	Pending Event = iota
	// Scalar: a complete legal scalar value.
	Scalar
	// Invalid: one illegal unit is finished.
	Invalid
)

// Result is the outcome of feeding one byte.
type Result struct {
	Kind Event
	Rune rune
	// Size is the finished unit's byte length (Kind != Pending).
	Size int
	// Take reports whether the fed byte belongs to the finished unit.
	// When false the caller must reprocess that byte from state zero.
	Take bool
}

// Decoder is a stateful UTF-8 decoder; incomplete prefixes are buffered
// internally and previously buffered bytes are never re-examined.
type Decoder struct {
	pend int
	need int
	cp   rune
	lead byte
}

// Reset clears all buffered state.
func (d *Decoder) Reset() { d.pend, d.need, d.cp, d.lead = 0, 0, 0, 0 }

// Pending reports whether an incomplete prefix is buffered.
func (d *Decoder) Pending() bool { return d.pend > 0 }

// Feed consumes one byte and reports the resulting event.
func (d *Decoder) Feed(b byte) Result {
	if d.pend == 0 {
		if b < 0x80 {
			return Result{Kind: Scalar, Rune: rune(b), Size: 1, Take: true}
		}
		n := scalar.LeadLen(b)
		if n == 0 {
			return Result{Kind: Invalid, Size: 1, Take: true}
		}
		d.lead, d.need, d.pend, d.cp = b, n, 1, rune(b&(0x7F>>uint(n-1)))
		return Result{Kind: Pending}
	}
	k := d.pend // 1,2,3 -> the byte b would occupy position k
	size := d.pend
	ok := (k == 1 && scalar.Contin2(d.lead, b)) || (k > 1 && scalar.Contin(b))
	if !ok {
		d.Reset()
		if k == 1 || b < 0x80 {
			return Result{Kind: Invalid, Size: size, Take: false}
		}
		return Result{Kind: Invalid, Size: size + 1, Take: true}
	}
	d.cp = d.cp<<6 | rune(b&0x3F)
	d.pend++
	if d.pend < d.need {
		return Result{Kind: Pending}
	}
	r, n := d.cp, d.pend
	d.Reset()
	return Result{Kind: Scalar, Rune: r, Size: n, Take: true}
}

// Drain finishes the stream: a buffered incomplete prefix becomes one
// illegal unit (truncated input); its byte length is returned.
func (d *Decoder) Drain() (size int, ok bool) {
	if d.pend == 0 {
		return 0, true
	}
	size = d.pend
	d.Reset()
	return size, false
}

// Encode appends the UTF-8 encoding of scalar r to p.
func Encode(p []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(p, byte(r))
	case r < 0x800:
		return append(p, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
	case r < 0x10000:
		return append(p, 0xE0|byte(r>>12), 0x80|byte((r>>6)&0x3F),
			0x80|byte(r&0x3F))
	default:
		return append(p, 0xF0|byte(r>>18), 0x80|byte((r>>12)&0x3F),
			0x80|byte((r>>6)&0x3F), 0x80|byte(r&0x3F))
	}
}
