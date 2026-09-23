package u8

import "ontology/scalar"

type Unit struct {
	Value     scalar.Value
	Size      int
	Invalid   bool
	Incomplete bool
}

func cont(b byte) bool { return b&0xC0 == 0x80 }

func DecodeAt(p []byte, at int) Unit {
	if at >= len(p) {
		return Unit{}
	}
	b := p[at]
	switch {
	case b < 0x80:
		return Unit{Value: scalar.Value(b), Size: 1}
	case b < 0xC2:
		return Unit{Invalid: true, Size: 1}
	case b < 0xE0:
		return two(p[at:])
	case b < 0xF0:
		return three(p[at:])
	default:
		return four(p[at:])
	}
}

func two(p []byte) Unit {
	if len(p) < 2 {
		return Unit{Size: len(p), Incomplete: true}
	}
	if !cont(p[1]) {
		return Unit{Invalid: true, Size: 1}
	}
	return Unit{Value: scalar.Value(p[0]&0x1F)<<6 | scalar.Value(p[1]&0x3F), Size: 2}
}

func three(p []byte) Unit {
	if len(p) < 2 {
		return Unit{Size: len(p), Incomplete: true}
	}
	lo, hi := byte(0x80), byte(0xBF)
	if p[0] == 0xE0 {
		lo = 0xA0
	}
	if p[0] == 0xED {
		hi = 0x9F
	}
	if p[1] < lo || p[1] > hi {
		return Unit{Invalid: true, Size: 1}
	}
	if len(p) < 3 {
		return Unit{Size: len(p), Incomplete: true}
	}
	if !cont(p[2]) {
		return Unit{Invalid: true, Size: 2}
	}
	v := scalar.Value(p[0]&0x0F)<<12 | scalar.Value(p[1]&0x3F)<<6 | scalar.Value(p[2]&0x3F)
	return Unit{Value: v, Size: 3}
}

func four(p []byte) Unit {
	if p[0] > 0xF4 {
		return Unit{Invalid: true, Size: 1}
	}
	if len(p) < 2 {
		return Unit{Size: len(p), Incomplete: true}
	}
	lo, hi := byte(0x80), byte(0xBF)
	if p[0] == 0xF0 {
		lo = 0x90
	}
	if p[0] == 0xF4 {
		hi = 0x8F
	}
	if p[1] < lo || p[1] > hi {
		return Unit{Invalid: true, Size: 1}
	}
	if len(p) < 3 {
		return Unit{Size: len(p), Incomplete: true}
	}
	if !cont(p[2]) {
		return Unit{Invalid: true, Size: 2}
	}
	if len(p) < 4 {
		return Unit{Size: len(p), Incomplete: true}
	}
	if !cont(p[3]) {
		return Unit{Invalid: true, Size: 3}
	}
	v := scalar.Value(p[0]&0x07)<<18 | scalar.Value(p[1]&0x3F)<<12 |
		scalar.Value(p[2]&0x3F)<<6 | scalar.Value(p[3]&0x3F)
	return Unit{Value: v, Size: 4}
}

func Encode(v scalar.Value) []byte {
	r := rune(v)
	switch {
	case r < 0x80:
		return []byte{byte(r)}
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r&0x3F)}
	case r < 0x10000:
		return []byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6&0x3F), 0x80 | byte(r&0x3F)}
	default:
		return []byte{0xF0 | byte(r>>18), 0x80 | byte(r>>12&0x3F),
			0x80 | byte(r>>6&0x3F), 0x80 | byte(r&0x3F)}
	}
}

func BoundaryAfter(p []byte, cut int) int {
	start := cut - 3
	if start < 0 {
		start = 0
	}
	n := 0
	for start+n < cut && cont(p[start+n]) {
		n++
	}
	if n > 0 && start > 0 {
		l := p[start-1]
		need := 1
		if l >= 0xE0 {
			need = 2
		}
		if l >= 0xF0 {
			need = 3
		}
		if l >= 0xC2 && l <= 0xF4 && n <= need {
			start--
		}
	}
	at := start
	if at < cut {
		for {
			u := DecodeAt(p, at)
			if at+u.Size <= cut {
				at += u.Size
				continue
			}
			if at < cut && u.Incomplete {
				return len(p)
			}
			at += u.Size
			break
		}
}
	return at
}
