package u16

import "ontology/scalar"

const MaxPending = 1

type Endian uint8

const (
	LittleEndian Endian = iota
	BigEndian
)

type Event struct {
	R         rune
	Invalid   bool
	Truncated bool
	Size      int
}

type Decoder struct {
	endian Endian
	first  byte
	have   bool
	high   rune
}

func NewDecoder(endian Endian) *Decoder { return &Decoder{endian: endian} }

func (d *Decoder) Push(p []byte, emit func(Event) bool) int {
	for i := 0; i < len(p); i++ {
		if !d.have {
			d.first, d.have = p[i], true
			continue
		}
		unit := d.unit(p[i])
		d.have = false
		switch {
		case scalar.HighSurrogate(unit):
			if d.high != 0 {
				if !emit(Event{R: scalar.Replacement, Invalid: true, Size: 2}) {
					return i - 1
				}
			}
			d.high = unit
		case scalar.LowSurrogate(unit):
			if d.high == 0 {
				if !emit(Event{R: scalar.Replacement, Invalid: true, Size: 2}) {
					return i - 1
				}
			} else {
				r, _ := scalar.SurrogatePair(d.high, unit)
				if !emit(Event{R: r, Size: 4}) {
					return i - 1
				}
				d.high = 0
			}
		default:
			if d.high != 0 {
				stop := !emit(Event{R: scalar.Replacement, Invalid: true, Size: 2})
				d.high = 0
				i--
				if stop {
					return i + 1
				}
			} else {
				if !emit(Event{R: unit, Size: 2}) {
					return i - 1
				}
			}
		}
	}
	pending := 0
	if d.high != 0 {
		pending += 2
	}
	if d.have {
		pending++
	}
	return len(p) - pending
}

func (d *Decoder) EOF(emit func(Event) bool) {
	if d.high != 0 {
		d.high = 0
		emit(Event{R: scalar.Replacement, Invalid: true, Truncated: true, Size: 2})
	}
	if d.have {
		d.have = false
		emit(Event{R: scalar.Replacement, Invalid: true, Truncated: true, Size: 1})
	}
}

func (d *Decoder) unit(second byte) rune {
	if d.endian == LittleEndian {
		return rune(second)<<8 | rune(d.first)
	}
	return rune(d.first)<<8 | rune(second)
}

func Encode(r rune, endian Endian) []byte {
	units := []uint16{uint16(r)}
	if high, low, ok := scalar.EncodeSurrogatePair(r); ok {
		units = []uint16{uint16(high), uint16(low)}
	}
	out := make([]byte, len(units)*2)
	for i, unit := range units {
		if endian == LittleEndian {
			out[i*2], out[i*2+1] = byte(unit), byte(unit>>8)
		} else {
			out[i*2], out[i*2+1] = byte(unit>>8), byte(unit)
		}
	}
	return out
}

func BOM(endian Endian) []byte { return Encode(scalar.ByteOrderMark, endian) }
