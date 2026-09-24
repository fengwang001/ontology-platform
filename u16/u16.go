package u16

import "ontology/scalar"

const BOM = 0xFEFF

type Event struct {
	R       rune
	Invalid bool
	Size    int
	BOM     bool
}

type Decoder struct {
	little      bool
	started     bool
	odd         bool
	first       byte
	high        rune
	highPending bool
	queue       [2]byte
	queued      bool
	checks      int64
}

func NewDecoder(littleEndian bool) *Decoder {
	return &Decoder{little: littleEndian}
}

func (d *Decoder) Reset() { little := d.little; *d = Decoder{little: little} }
func (d *Decoder) Checks() int64 { return d.checks }

func (d *Decoder) Pop() (Event, bool) {
	if !d.queued {
		return Event{}, false
	}
	d.queued = false
	return d.code(d.decode(d.queue[0], d.queue[1])), true
}

func (d *Decoder) Accept(b byte) (Event, bool) {
	d.checks++
	if !d.odd {
		d.first, d.odd = b, true
		return Event{}, false
	}
	d.odd = false
	first, second := d.first, b
	if !d.started {
		d.started = true
		if first == 0xFF && second == 0xFE {
			d.little = true
			return Event{R: BOM, Size: 2, BOM: true}, true
		}
		if first == 0xFE && second == 0xFF {
			d.little = false
			return Event{R: BOM, Size: 2, BOM: true}, true
		}
	}
	return d.code(d.decode(first, second)), true
}

func (d *Decoder) decode(first, second byte) rune {
	if d.little {
		return rune(first) | rune(second)<<8
	}
	return rune(first)<<8 | rune(second)
}

func (d *Decoder) code(code rune) Event {
	switch {
	case scalar.IsHighSurrogate(code):
		d.high, d.highPending = code, true
		return Event{}
	case d.highPending && scalar.IsLowSurrogate(code):
		r, _ := scalar.DecodeSurrogate(d.high, code)
		d.highPending = false
		return Event{R: r, Size: 4}
	case d.highPending:
		d.highPending = false
		d.queue[0], d.queue[1], d.queued = byte(code&0xFF), byte(code>>8), true
		return Event{R: scalar.Replacement, Invalid: true, Size: 2}
	case scalar.IsLowSurrogate(code):
		return Event{R: scalar.Replacement, Invalid: true, Size: 2}
	case scalar.IsSurrogate(code):
		return Event{R: scalar.Replacement, Invalid: true, Size: 2}
	default:
		return Event{R: code, Size: 2}
	}
}

func (d *Decoder) End() (Event, bool) {
	if d.queued {
		d.queued = false
		return d.code(d.decode(d.queue[0], d.queue[1])), true
	}
	if d.odd {
		d.odd = false
		return Event{R: scalar.Replacement, Invalid: true, Size: 1}, true
	}
	if d.highPending {
		d.highPending = false
		return Event{R: scalar.Replacement, Invalid: true, Size: 2}, true
	}
	return Event{}, false
}

func Encode(r rune, littleEndian bool) ([]byte, bool) {
	if !scalar.IsScalar(r) {
		return nil, false
	}
	put := func(code rune) []byte {
		if littleEndian {
			return []byte{byte(code), byte(code >> 8)}
		}
		return []byte{byte(code >> 8), byte(code)}
	}
	if high, low, ok := scalar.EncodeSurrogate(r); ok {
		return append(put(high), put(low)...), true
	}
	return put(r), true
}
