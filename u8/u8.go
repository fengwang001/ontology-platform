package u8

import (
	"errors"

	"ontology/scalar"
)

var (
	ErrInvalid   = errors.New("invalid UTF-8 unit")
	ErrTruncated = errors.New("truncated UTF-8 sequence")
)

type Event struct {
	R     rune
	Size  int
	Count int
	Err   error
}

type Decoder struct {
	buf    [4]byte
	n      int
	need   int
	dead   bool
	checks int
}

func (d *Decoder) Checks() int { return d.checks }

func (d *Decoder) Put(b byte) (Event, bool, bool) {
	d.checks++
	if d.n == 0 {
		switch {
		case b < 0x80:
			return Event{R: rune(b), Size: 1, Count: 1}, true, true
		case b >= 0xC2 && b <= 0xDF:
			d.start(b, 2)
		case b >= 0xE0 && b <= 0xEF:
			d.start(b, 3)
		case b >= 0xF0 && b <= 0xF4:
			d.start(b, 4)
		default:
			return Event{R: scalar.Replacement, Size: 1, Count: 1, Err: ErrInvalid}, true, true
		}
		return Event{}, false, true
	}
	d.buf[d.n] = b
	d.n++
	if b < 0x80 || b > 0xBF {
		e := d.badPrefix()
		return e, true, false
	}
	if d.n == 2 {
		d.dead = !secondOK(d.buf[0], b)
	}
	if d.n == d.need {
		if d.dead {
			return d.badComplete(), true, true
		}
		r := decode(d.buf[:d.need])
		if !scalar.Valid(r) {
			return d.badComplete(), true, true
		}
		e := Event{R: r, Size: d.need, Count: 1}
		d.reset()
		return e, true, true
	}
	return Event{}, false, true
}

func (d *Decoder) Close() (Event, bool) {
	if d.n == 0 {
		return Event{}, false
	}
	if d.dead {
		e := d.badPrefix()
		d.reset()
		return e, true
	}
	e := Event{R: scalar.Replacement, Size: d.n, Count: 1, Err: ErrTruncated}
	d.reset()
	return e, true
}

func (d *Decoder) start(b byte, need int) {
	d.buf[0], d.n, d.need, d.dead = b, 1, need, false
}

func (d *Decoder) reset() { d.n, d.need, d.dead = 0, 0, false }

func (d *Decoder) badPrefix() Event {
	e := Event{R: scalar.Replacement, Size: d.n, Count: 1, Err: ErrInvalid}
	d.reset()
	return e
}

func (d *Decoder) badComplete() Event {
	e := Event{R: scalar.Replacement, Size: d.n, Count: d.n, Err: ErrInvalid}
	d.reset()
	return e
}

func secondOK(first, second byte) bool {
	switch first {
	case 0xE0:
		return second >= 0xA0
	case 0xED:
		return second <= 0x9F
	case 0xF0:
		return second >= 0x90
	case 0xF4:
		return second <= 0x8F
	default:
		return true
	}
}

func decode(b []byte) rune {
	switch len(b) {
	case 2:
		return rune(b[0]&0x1F)<<6 | rune(b[1]&0x3F)
	case 3:
		return rune(b[0]&0x0F)<<12 | rune(b[1]&0x3F)<<6 | rune(b[2]&0x3F)
	default:
		return rune(b[0]&0x07)<<18 | rune(b[1]&0x3F)<<12 | rune(b[2]&0x3F)<<6 | rune(b[3]&0x3F)
	}
}

func Encode(r rune) []byte {
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

func AlignCut(data []byte, cut int) (int, int) {
	start := cut - 3
	if start < 0 {
		start = 0
	}
	checks := 0
	for p := start; p < cut; p++ {
		first := data[p]
		checks++
		need := 0
		if first >= 0xC2 && first <= 0xDF {
			need = 2
		} else if first >= 0xE0 && first <= 0xEF {
			need = 3
		} else if first >= 0xF0 && first <= 0xF4 {
			need = 4
		}
		if need == 0 || p+need <= cut {
			continue
		}
		end := p + need
		if end > len(data) {
			end = len(data)
		}
		for q := p + 1; q < end; q++ {
			checks++
			if data[q] < 0x80 || data[q] > 0xBF || (q == p+1 && !secondOK(first, data[q])) {
				end = q
				break
			}
		}
		if cut < end {
			return end, checks
		}
	}
	return cut, checks
}
