// Package u8 implements byte-level incremental UTF-8 decoding and encoding.
package u8

import "ontology/scalar"

const (
	KindRune = iota
	KindInvalid
)

// Event is one decoded scalar or one invalid unit.
type Event struct {
	Kind int
	Rune rune
	Size int // bytes of the input unit consumed
}

// MaxPending is the hard upper bound on buffered input bytes.
const MaxPending = 6 // up to 3 continuations + up to 3 re-fed bytes

// Decoder decodes one byte at a time; events queue inside it.
type Decoder struct {
	state  int // expected total length, 0 when idle
	lead   byte
	need   int
	cont   []byte // accepted continuation bytes (cap 3)
	retry  []byte // FIFO of bytes to re-feed after a broken prefix (cap 3)
	events []Event
	checks int64
}

func cont(b byte) bool { return b&0xC0 == 0x80 }

// Pending reports buffered input bytes (bound: MaxPending).
func (d *Decoder) Pending() int { return len(d.cont) + len(d.retry) }

// Checks counts input bytes inspected since construction (each once).
func (d *Decoder) Checks() int64 { return d.checks }

// Feed feeds exactly one input byte; drain queued events with Poll.
func (d *Decoder) Feed(b byte) {
	d.checks++
	for {
		if d.state == 0 {
			d.start(b)
		} else {
			d.step(b)
		}
		if len(d.retry) == 0 {
			return
		}
		b = d.retry[0]
		copy(d.retry, d.retry[1:])
		d.retry = d.retry[:len(d.retry)-1]
	}
}

func (d *Decoder) start(b byte) {
	switch {
	case b < 0x80:
		d.events = append(d.events, Event{KindRune, rune(b), 1})
	case b < 0xC2 || b >= 0xF5: // 80..BF, C0, C1, F5..FF
		d.events = append(d.events, Event{Kind: KindInvalid, Size: 1})
	case b <= 0xDF:
		d.state, d.need, d.lead = 2, 1, b
	case b <= 0xEF:
		d.state, d.need, d.lead = 3, 2, b
	default:
		d.state, d.need, d.lead = 4, 3, b
	}
}

// secondOK reports the restricted second-byte range for special leads.
func secondOK(lead, b byte) bool {
	switch lead {
	case 0xE0:
		return b >= 0xA0
	case 0xED:
		return b <= 0x9F
	case 0xF0:
		return b >= 0x90
	case 0xF4:
		return b <= 0x8F
	default:
		return true // 80..BF already checked by caller
	}
}

func (d *Decoder) step(b byte) {
	if !cont(b) || (d.need == d.state-1 && !secondOK(d.lead, b)) {
		size := len(d.cont) + 1
		// The whole broken prefix (including accepted continuations)
		// is swallowed as one invalid unit; only the breaking byte b
		// is re-fed as the start of a new unit.
		d.cont = d.cont[:0]
		d.state = 0
		d.events = append(d.events, Event{Kind: KindInvalid, Size: size})
		d.retry = append(d.retry, b)
	}
	d.cont = append(d.cont, b)
	d.need--
	if d.need == 0 {
		d.emit()
	}
}

func (d *Decoder) emit() {
	r := decodeCont(d.lead, d.cont)
	size := len(d.cont) + 1
	d.cont = d.cont[:0]
	d.state = 0
	d.events = append(d.events, Event{KindRune, r, size})
}

func decodeCont(lead byte, cs []byte) rune {
	switch len(cs) + 1 {
	case 2:
		return rune(lead&0x1F)<<6 | rune(cs[0]&0x3F)
	case 3:
		return rune(lead&0x0F)<<12 | rune(cs[0]&0x3F)<<6 | rune(cs[1]&0x3F)
	default:
		return rune(lead&0x07)<<18 | rune(cs[0]&0x3F)<<12 |
			rune(cs[1]&0x3F)<<6 | rune(cs[2]&0x3F)
	}
}

// Poll removes and returns the next event.
func (d *Decoder) Poll() (Event, bool) {
	if len(d.events) == 0 {
		return Event{}, false
	}
	e := d.events[0]
	d.events = d.events[1:]
	return e, true
}

// Close flushes a residual prefix as one invalid unit of its byte length.
func (d *Decoder) Close() (int, bool) {
	if d.state == 0 {
		return 0, false
	}
	size := len(d.cont) + 1
	d.cont = d.cont[:0]
	d.state = 0
	d.events = append(d.events, Event{Kind: KindInvalid, Size: size})
	return size, true
}

// Len returns the UTF-8 byte length of a valid scalar.
func Len(r rune) int {
	switch {
	case r < 0x80:
		return 1
	case r < 0x800:
		return 2
	case r < 0x10000:
		return 3
	default:
		return 4
	}
}

// Encode appends the UTF-8 encoding of r to p.
func Encode(p []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(p, byte(r))
	case r < 0x800:
		return append(p, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
	case r < 0x10000:
		return append(p, 0xE0|byte(r>>12), 0x80|byte((r>>6)&0x3F), 0x80|byte(r&0x3F))
	default:
		return append(p, 0xF0|byte(r>>18), 0x80|byte((r>>12)&0x3F),
			0x80|byte((r>>6)&0x3F), 0x80|byte(r&0x3F))
	}
}

var _ = scalar.MaxValue
