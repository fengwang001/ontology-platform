package u8

import "ontology/scalar"

type Event struct {
	R        scalar.Value
	Start    int
	Len      int
	Invalid  bool
	Complete bool
}

type Decoder struct {
	pending []byte
	need    int
	offset  int
	checks  int
}

func NewDecoder() *Decoder { return &Decoder{} }

func (d *Decoder) Checks() int     { return d.checks }
func (d *Decoder) Offset() int     { return d.offset }
func (d *Decoder) PendingLen() int { return len(d.pending) }

type State struct {
	Pending []byte
	Need    int
	Offset  int
	Checks  int
}

func (d *Decoder) Save() State {
	return State{append([]byte(nil), d.pending...), d.need, d.offset, d.checks}
}

func (d *Decoder) Restore(s State) {
	d.pending = append(d.pending[:0], s.Pending...)
	d.need, d.offset, d.checks = s.Need, s.Offset, s.Checks
}

func continuation(b byte) bool { return b >= 0x80 && b <= 0xBF }

func leadWindow(lead byte) (byte, byte) {
	switch {
	case lead == 0xE0 || lead == 0xF0:
		return 0xA0, 0xBF
	case lead == 0xED:
		return 0x80, 0x9F
	case lead == 0xF4:
		return 0x80, 0x8F
	default:
		return 0x80, 0xBF
	}
}

func decodePending(p []byte) scalar.Value {
	r := scalar.Value(p[0] & (0xFF >> len(p)))
	for _, b := range p[1:] {
		r = r<<6 | scalar.Value(b&0x3F)
	}
	return r
}

func (d *Decoder) startLead(b byte) (Event, bool) {
	switch {
	case b < 0x80:
		return Event{R: scalar.Value(b), Start: d.offset, Len: 1, Complete: true}, true
	case b >= 0xC2 && b <= 0xDF:
		d.need = 2
	case b == 0xE0, b >= 0xE1 && b <= 0xEF:
		d.need = 3
	case b == 0xF0, b >= 0xF1 && b <= 0xF4:
		d.need = 4
	default:
		return Event{Start: d.offset, Len: 1, Invalid: true, Complete: true}, true
	}
	d.pending = append(d.pending[:0], b)
	return Event{}, false
}

func (d *Decoder) Feed(b byte) (Event, int) {
	d.checks++
	if len(d.pending) == 0 {
		e, ready := d.startLead(b)
		if ready {
			d.offset++
		}
		return e, 1
	}
	lead := d.pending[0]
	if !continuation(b) || (len(d.pending) == 1 && func() bool {
		low, high := leadWindow(lead)
		return b < low || b > high
	}()) {
		e := Event{Start: d.offset, Len: len(d.pending), Invalid: true, Complete: true}
		d.offset += len(d.pending)
		d.pending, d.need = nil, 0
		return e, 0
	}
	d.pending = append(d.pending, b)
	if len(d.pending) < d.need {
		return Event{}, 1
	}
	r := decodePending(d.pending)
	e := Event{R: r, Start: d.offset, Len: len(d.pending), Invalid: !scalar.IsScalar(r), Complete: true}
	d.offset += len(d.pending)
	d.pending, d.need = nil, 0
	return e, 1
}

func (d *Decoder) Close() (Event, bool) {
	if len(d.pending) == 0 {
		return Event{}, false
	}
	e := Event{Start: d.offset, Len: len(d.pending), Invalid: true, Complete: true}
	d.offset += len(d.pending)
	d.pending, d.need = nil, 0
	return e, true
}

func Encode(r scalar.Value) []byte {
	switch {
	case r < 0x80:
		return []byte{byte(r)}
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r&0x3F)}
	case r < 0x10000:
		return []byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6&0x3F), 0x80 | byte(r&0x3F)}
	default:
		return []byte{0xF0 | byte(r>>18), 0x80 | byte(r>>12&0x3F), 0x80 | byte(r>>6&0x3F), 0x80 | byte(r&0x3F)}
	}
}

func StartAt(data []byte, cut int) int {
	if cut == 0 || cut >= len(data) {
		return cut
	}
	start := cut
	for j := cut - 1; j >= 0 && j >= cut-3; j-- {
		b := data[j]
		if b < 0x80 || b >= 0xC0 {
			need := 0
			if b >= 0xC2 && b <= 0xDF {
				need = 2
			}
			if b >= 0xE0 && b <= 0xEF {
				need = 3
			}
			if b >= 0xF0 && b <= 0xF4 {
				need = 4
			}
			if need != 0 && cut-j < need {
				ok := true
				for k := j + 1; k < cut; k++ {
					ok = ok && continuation(data[k])
					if k == j+1 {
						low, high := leadWindow(b)
						ok = ok && data[k] >= low && data[k] <= high
					}
				}
				if ok {
					start = j
				}
			}
			break
		}
		start = j
	}
	return start
}
