package u16

import "ontology/scalar"

type Order uint8

const (
	Auto Order = iota
	LittleEndian
	BigEndian
)

type Event struct {
	R         scalar.Value
	Start     int
	Len       int
	Invalid   bool
	Truncated bool
	Complete  bool
}

type Decoder struct {
	order   Order
	pending []byte
	high    scalar.Value
	highAt  int
	hasHigh bool
	offset  int
	checks  int
}

func NewDecoder(order Order) *Decoder { return &Decoder{order: order} }

func (d *Decoder) Checks() int     { return d.checks }
func (d *Decoder) Offset() int     { return d.offset }
func (d *Decoder) PendingLen() int { return len(d.pending) }
func (d *Decoder) Order() Order    { return d.order }

type State struct {
	Order   Order
	Pending []byte
	High    scalar.Value
	HighAt  int
	HasHigh bool
	Offset  int
	Checks  int
}

func (d *Decoder) Save() State {
	return State{d.order, append([]byte(nil), d.pending...), d.high, d.highAt, d.hasHigh, d.offset, d.checks}
}

func (d *Decoder) Restore(s State) {
	d.order, d.high, d.highAt, d.hasHigh = s.Order, s.High, s.HighAt, s.HasHigh
	d.offset, d.checks = s.Offset, s.Checks
	d.pending = append(d.pending[:0], s.Pending...)
}

func unit(p []byte, order Order) scalar.Value {
	if order == BigEndian {
		return scalar.Value(p[0])<<8 | scalar.Value(p[1])
	}
	return scalar.Value(p[1])<<8 | scalar.Value(p[0])
}

func (d *Decoder) scalarUnit(u scalar.Value, start int) (Event, bool) {
	switch {
	case scalar.IsLowSurrogate(u):
		return Event{Start: start, Len: 2, Invalid: true, Complete: true}, true
	case scalar.IsHighSurrogate(u):
		d.high, d.hasHigh, d.highAt = u, true, start
		return Event{}, false
	default:
		return Event{R: u, Start: start, Len: 2, Complete: true}, true
	}
}

func (d *Decoder) unitEvent(p []byte) (Event, int) {
	start := d.offset
	if d.order == Auto {
		u := unit(p, LittleEndian)
		if u == 0xFEFF {
			d.order = LittleEndian
		} else if unit(p, BigEndian) == 0xFEFF {
			d.order, u = BigEndian, 0xFEFF
		} else {
			d.order = LittleEndian
		}
		if u == 0xFEFF {
			d.offset += 2
			return Event{R: u, Start: start, Len: 2, Complete: true}, 2
		}
	}
	u := unit(p, d.order)
	if d.hasHigh {
		if scalar.IsLowSurrogate(u) {
			r := scalar.SurrogatePair(d.high, u)
			d.high, d.hasHigh = 0, false
			d.offset = d.highAt + 4
			return Event{R: r, Start: d.highAt, Len: 4, Complete: true}, 2
		}
		d.high, d.hasHigh = 0, false
		d.pending = p
		return Event{Start: d.highAt, Len: 2, Invalid: true, Complete: true}, 0
	}
	e, ready := d.scalarUnit(u, start)
	if ready {
		d.offset += 2
	}
	return e, 2
}

func (d *Decoder) Retry() (Event, int) {
	if len(d.pending) != 2 {
		return Event{}, 0
	}
	p := d.pending
	d.pending = nil
	return d.unitEvent(p)
}

func (d *Decoder) Feed(b byte) (Event, int) {
	d.checks++
	d.pending = append(d.pending, b)
	if len(d.pending) < 2 {
		return Event{}, 1
	}
	p := d.pending
	d.pending = nil
	return d.unitEvent(p)
}

func (d *Decoder) Close() (Event, bool) {
	switch {
	case len(d.pending) == 1:
		e := Event{Start: d.offset, Len: 1, Invalid: true, Truncated: true, Complete: true}
		d.offset++
		d.pending = nil
		return e, true
	case d.hasHigh:
		e := Event{Start: d.highAt, Len: 2, Invalid: true, Truncated: true, Complete: true}
		d.high, d.hasHigh = 0, false
		return e, true
	}
	return Event{}, false
}

func Encode(r scalar.Value, order Order) []byte {
	units := []scalar.Value{r}
	if r >= 0x10000 {
		hi, lo := scalar.EncodeSurrogatePair(r)
		units = []scalar.Value{hi, lo}
	}
	out := make([]byte, 0, 4)
	for _, u := range units {
		if order == BigEndian {
			out = append(out, byte(u>>8), byte(u))
		} else {
			out = append(out, byte(u), byte(u>>8))
		}
	}
	return out
}
