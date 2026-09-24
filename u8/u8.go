package u8

import "ontology/scalar"

type Unit struct {
	R          rune
	Size       int
	Bad        int
	Want       int
	Valid      bool
	Incomplete bool
}

func Cont(b byte) bool { return b&0xC0 == 0x80 }

func LeadLen(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b >= 0xC2 && b <= 0xDF:
		return 2
	case b >= 0xE0 && b <= 0xEF:
		return 3
	case b >= 0xF0 && b <= 0xF4:
		return 4
	default:
		return 1
	}
}

func SecondOK(first, second byte) bool {
	if !Cont(second) {
		return false
	}
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
		return first >= 0xC2 && first <= 0xF4
	}
}

func Decode(p []byte) Unit {
	if len(p) == 0 {
		return Unit{}
	}
	first := p[0]
	if first < 0x80 {
		return Unit{R: rune(first), Size: 1, Valid: true}
	}
	want := LeadLen(first)
	if want == 1 || (want >= 2 && (len(p) < 2 || !SecondOK(first, p[1]))) {
		return Unit{Size: 1, Bad: 1, Want: want}
	}
	r := rune(first&(0x7F>>uint(want))) << (6 * uint(want-1))
	r |= rune(p[1]&0x3F) << (6 * uint(want-2))
	for i := 2; i < want; i++ {
		if len(p) <= i {
			return Unit{Size: len(p), Want: want, Incomplete: true}
		}
		if !Cont(p[i]) {
			return Unit{Size: i + 1, Bad: i + 1}
		}
		r |= rune(p[i]&0x3F) << (6 * uint(want-1-i))
	}
	if !scalar.Valid(r) {
		return Unit{Size: want, Bad: want}
	}
	return Unit{R: r, Size: want, Valid: true}
}

func Step(p []byte) int {
	u := Decode(p)
	if u.Incomplete {
		if u.Want < len(p) {
			return u.Want
		}
		return len(p)
	}
	return u.Size
}

func Encode(r rune) []byte {
	if !scalar.Valid(r) {
		r = scalar.Replacement
	}
	switch {
	case r < 0x80:
		return []byte{byte(r)}
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r&0x3F)}
	case r < 0x10000:
		return []byte{0xE0 | byte(r>>12), 0x80 | byte((r>>6)&0x3F), 0x80 | byte(r&0x3F)}
	default:
		return []byte{0xF0 | byte(r>>18), 0x80 | byte((r>>12)&0x3F), 0x80 | byte((r>>6)&0x3F), 0x80 | byte(r&0x3F)}
	}
}
