package u8

import "ontology/scalar"

const MaxPending = 3

type Unit struct {
	R     rune
	Size  int
	Valid bool
}

type Decoder struct {
	pend [MaxPending]byte
	cp   rune
	n    int
	chk  int64
}

func NewDecoder() *Decoder       { return &Decoder{} }
func (d *Decoder) Checks() int64 { return d.chk }
func (d *Decoder) Pending() int  { return d.n }

func (d *Decoder) Feed(b byte) ([2]Unit, int) {
	d.chk++
	if d.n > 0 {
		if !d.continues(b) {
			size := d.n
			d.reset()
			var us [2]Unit
			us[0] = Unit{Size: size}
			next, n := d.Feed(b)
			us[1] = next[0]
			return us, 1 + n
		}
		d.pend[d.n] = b
		d.n++
		d.cp = d.cp<<6 | rune(b&0x3f)
		if d.n == d.expected() {
			u := Unit{R: d.cp, Size: d.n, Valid: scalar.IsScalar(d.cp)}
			d.reset()
			return [2]Unit{u}, 1
		}
	}
	switch {
	case b < 0x80:
		return [2]Unit{{R: rune(b), Size: 1, Valid: true}}, 1
	case b < 0xc2:
		return [2]Unit{{Size: 1}}, 1
	case b < 0xe0:
		d.begin(b, rune(b&0x1f), 2)
	case b < 0xf0:
		d.begin(b, rune(b&0x0f), 3)
	case b < 0xf5:
		d.begin(b, rune(b&0x07), 4)
	default:
		return [2]Unit{{Size: 1}}, 1
	}
	return [2]Unit{}, 0
}

func (d *Decoder) Finish() (Unit, bool) {
	if d.n == 0 {
		return Unit{}, false
	}
	u := Unit{Size: d.n}
	d.reset()
	return u, true
}

func Encode(dst []byte, r rune) ([]byte, bool) {
	if !scalar.IsScalar(r) {
		return dst, false
	}
	switch {
	case r < 0x80:
		return append(dst, byte(r)), true
	case r < 0x800:
		return append(dst, 0xc0|byte(r>>6), 0x80|byte(r&0x3f)), true
	case r < 0x10000:
		return append(dst, 0xe0|byte(r>>12), 0x80|byte((r>>6)&0x3f), 0x80|byte(r&0x3f)), true
	default:
		return append(dst, 0xf0|byte(r>>18), 0x80|byte((r>>12)&0x3f), 0x80|byte((r>>6)&0x3f), 0x80|byte(r&0x3f)), true
	}
}

func OverlapStart(prev []byte, at byte) int {
	for k := 1; k <= MaxPending && k <= len(prev); k++ {
		d := NewDecoder()
		var n int
		for _, b := range prev[len(prev)-k:] {
			_, n = d.Feed(b)
			if n != 0 || d.Pending() == 0 {
				break
			}
		}
		if n == 0 && d.Pending() == k {
			_, n = d.Feed(at)
			if n == 0 && d.Pending() == k+1 {
				return k
			}
		}
	}
	return 0
}

func (d *Decoder) begin(first byte, cp rune, n int) {
	d.pend[0], d.cp, d.n = first, cp, 1
}

func (d *Decoder) expected() int {
	switch d.pend[0] >> 4 {
	case 0xc, 0xd:
		return 2
	case 0xe:
		return 3
	default:
		return 4
	}
}

func (d *Decoder) continues(b byte) bool {
	if b < 0x80 || b > 0xbf {
		return false
	}
	if d.n == 1 {
		switch d.pend[0] {
		case 0xe0:
			return b >= 0xa0
		case 0xed:
			return b <= 0x9f
		case 0xf0:
			return b >= 0x90
		case 0xf4:
			return b <= 0x8f
		}
	}
	return true
}

func (d *Decoder) reset() { *d = Decoder{chk: d.chk} }
