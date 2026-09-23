package u16

import "ontology/scalar"

type Order int

const (
	LittleEndian Order = iota
	BigEndian
)

type Event struct {
	R      scalar.Value
	OK     bool
	Len    int
	Offset int64
	Checks int
}

type Decoder struct {
	order     Order
	auto      bool
	orderSet  bool
	first     int64
	odd       bool
	low       byte
	high      scalar.Value
	highStart int64
	oddStart  int64
}

func NewDecoder(order Order, auto bool, start int64) Decoder {
	return Decoder{order: order, auto: auto, first: start}
}

func (d *Decoder) Pending() bool { return d.odd || d.high != 0 }

func (d *Decoder) Order() Order { return d.order }

func (d *Decoder) StepAt(b byte, off int64) (Event, Event, bool) {
	if d.odd {
		var unit uint16
		if d.order == LittleEndian {
			unit = uint16(b)<<8 | uint16(d.low)
		} else {
			unit = uint16(d.low)<<8 | uint16(b)
		}
		d.odd = false
		if d.auto && !d.orderSet && off-1 == d.first {
			switch unit {
			case 0xFEFF:
				d.orderSet = true
				return Event{R: scalar.Value(unit), OK: true, Len: 2, Offset: off - 1, Checks: 1}, Event{}, true
			case 0xFFFE:
				d.order = 1 - d.order
				d.orderSet = true
			}
		}
		d.orderSet = true
		ev, next := d.unit(unit, off-1)
		return ev, next, false
	}
	d.odd, d.low, d.oddStart = true, b, off
	return Event{}, Event{}, false
}

func (d *Decoder) Step(b byte) (Event, Event, bool) { return d.StepAt(b, 0) }

func (d *Decoder) unit(unit uint16, off int64) (Event, Event) {
	r := scalar.Value(unit)
	if d.high != 0 {
		if scalar.LowSurrogate(r) {
			pair, _ := scalar.SurrogatePair(d.high, r)
			start := d.highStart
			d.high = 0
			return Event{R: pair, OK: true, Len: 4, Offset: start, Checks: 1}, Event{}
		}
		invalid := Event{R: scalar.Replacement, Len: 2, Offset: d.highStart, Checks: 1}
		d.high = 0
		next, extra := d.single(unit, off)
		next.Checks = 0
		if next.Len == 0 {
			next = extra
		}
		return invalid, next
	}
	return d.single(unit, off)
}

func (d *Decoder) single(unit uint16, off int64) (Event, Event) {
	r := scalar.Value(unit)
	ev := Event{R: r, Len: 2, Offset: off, Checks: 1}
		if scalar.HighSurrogate(r) {
			d.high, d.highStart = r, off
			return Event{Checks: 1}, Event{}
		}
	if scalar.LowSurrogate(r) {
		ev.R = scalar.Replacement
		return ev, Event{}
	}
	ev.OK = scalar.Valid(r)
	return ev, Event{}
}

func (d *Decoder) Close() (Event, bool, bool) {
	if d.odd {
		d.odd = false
		return Event{R: scalar.Replacement, Len: 1, Offset: d.oddStart}, true, false
	}
	if d.high != 0 {
		d.high = 0
		return Event{R: scalar.Replacement, Len: 2, Offset: d.highStart}, true, true
	}
	return Event{}, false, false
}

func Encode(r scalar.Value, order Order) []byte {
	if r >= 0x10000 {
		r -= 0x10000
		high := 0xD800 + scalar.Value(r>>10)
		low := 0xDC00 + scalar.Value(r&0x3FF)
		return append(unit(high, order), unit(low, order)...)
	}
	return unit(r, order)
}

func unit(r scalar.Value, order Order) []byte {
	if order == LittleEndian {
		return []byte{byte(r), byte(r >> 8)}
	}
	return []byte{byte(r >> 8), byte(r)}
}
