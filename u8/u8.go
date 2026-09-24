package u8

const MaxPending = 3

type Event struct {
	R         rune
	Invalid   bool
	Truncated bool
	Size      int
}

type Decoder struct {
	buf   [MaxPending + 1]byte
	total int
	got   int
}

func Pending() int { return MaxPending }

func SecondOK(first, second byte) bool {
	switch {
	case first == 0xE0:
		return second >= 0xA0 && second <= 0xBF
	case first == 0xED:
		return second >= 0x80 && second <= 0x9F
	case first == 0xF0:
		return second >= 0x90 && second <= 0xBF
	case first == 0xF4:
		return second >= 0x80 && second <= 0x8F
	case first >= 0xC2 && first <= 0xDF,
		first >= 0xE1 && first <= 0xEC,
		first >= 0xEE && first <= 0xEF,
		first >= 0xF1 && first <= 0xF3:
		return second >= 0x80 && second <= 0xBF
	default:
		return false
	}
}

func (d *Decoder) Push(p []byte, emit func(Event) bool) int {
	for i := 0; i < len(p); i++ {
		b := p[i]
		if d.total == 0 {
			switch {
			case b < 0x80:
				if !emit(Event{R: rune(b), Size: 1}) {
					return i + 1
				}
			case b >= 0xC2 && b <= 0xDF:
				d.total, d.got, d.buf[0] = 2, 1, b
			case b >= 0xE0 && b <= 0xEF:
				d.total, d.got, d.buf[0] = 3, 1, b
			case b >= 0xF0 && b <= 0xF4:
				d.total, d.got, d.buf[0] = 4, 1, b
			default:
				emit(Event{Invalid: true, Size: 1})
			}
			continue
		}
		if d.got == 1 && !SecondOK(d.buf[0], b) ||
			d.got > 1 && (b < 0x80 || b > 0xBF) {
			size := d.got
			d.total, d.got = 0, 0
			stop := !emit(Event{Invalid: true, Size: size})
			i--
			if stop {
				return i + 1
			}
			continue
		}
		d.buf[d.got] = b
		d.got++
		if d.got == d.total {
			if !emit(Event{R: decode(d.buf[:d.total])}) {
				return i + 1
			}
			d.total, d.got = 0, 0
		}
	}
	return len(p)
}

func (d *Decoder) EOF(emit func(Event) bool) {
	if d.total != 0 {
		size := d.got
		d.total, d.got = 0, 0
		emit(Event{Invalid: true, Truncated: true, Size: size})
	}
}

func decode(b []byte) rune {
	switch len(b) {
	case 4:
		return rune(b[0]&7)<<18 | rune(b[1]&63)<<12 | rune(b[2]&63)<<6 | rune(b[3]&63)
	case 3:
		return rune(b[0]&15)<<12 | rune(b[1]&63)<<6 | rune(b[2]&63)
	default:
		return rune(b[0]&31)<<6 | rune(b[1]&63)
	}
}

func Encode(r rune) []byte {
	switch {
	case r < 0x80:
		return []byte{byte(r)}
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r&63)}
	case r < 0x10000:
		return []byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6&63), 0x80 | byte(r&63)}
	default:
		return []byte{0xF0 | byte(r>>18), 0x80 | byte(r>>12&63),
			0x80 | byte(r>>6&63), 0x80 | byte(r&63)}
	}
}
