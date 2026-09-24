package u8

import "ontology/scalar"

type Unit struct {
	R     rune
	Size  int
	Valid bool
}

func Encode(r rune) []byte {
	if !scalar.IsValid(r) {
		r = scalar.Replacement
	}
	switch scalar.UTF8Len(r) {
	case 1:
		return []byte{byte(r)}
	case 2:
		return []byte{0xc0 | byte(r>>6), 0x80 | byte(r&0x3f)}
	case 3:
		return []byte{0xe0 | byte(r>>12), 0x80 | byte(r>>6&0x3f), 0x80 | byte(r&0x3f)}
	default:
		return []byte{0xf0 | byte(r>>18), 0x80 | byte(r>>12&0x3f), 0x80 | byte(r>>6&0x3f), 0x80 | byte(r&0x3f)}
	}
}

func Next(p []byte) Unit {
	if len(p) == 0 {
		return Unit{}
	}
	b0 := p[0]
	need, min2, max2 := LeadSpec(b0)
	if need == 0 {
		return Unit{Size: 1}
	}
	if len(p) < need {
		deadAt := deadPrefixAt(p, min2, max2)
		if deadAt < 0 {
			return Unit{Size: 0}
		}
		return Unit{Size: deadAt}
	}
	if len(p) < 2 || p[1] < min2 || p[1] > max2 || !continuation(p[2:need]) {
		return Unit{Size: 1}
	}
	r := value(b0, p[1:need], need)
	if !scalar.IsValid(r) {
		return Unit{Size: 1}
	}
	return Unit{R: r, Size: need, Valid: true}
}

func LeadSpec(b byte) (int, byte, byte) {
	switch {
	case b < 0x80:
		return 1, 0, 0
	case b < 0xc2:
		return 0, 0, 0
	case b < 0xe0:
		return 2, 0x80, 0xbf
	case b == 0xe0:
		return 3, 0xa0, 0xbf
	case b < 0xed:
		return 3, 0x80, 0xbf
	case b == 0xed:
		return 3, 0x80, 0x9f
	case b < 0xf0:
		return 3, 0x80, 0xbf
	case b == 0xf0:
		return 4, 0x90, 0xbf
	case b < 0xf4:
		return 4, 0x80, 0xbf
	case b == 0xf4:
		return 4, 0x80, 0x8f
	default:
		return 0, 0, 0
	}
}

func continuation(p []byte) bool {
	for _, b := range p {
		if b < 0x80 || b > 0xbf {
			return false
		}
	}
	return true
}

func deadPrefixAt(p []byte, min2, max2 byte) int {
	if len(p) > 1 && (p[1] < min2 || p[1] > max2) {
		return 1
	}
	for i := 2; i < len(p); i++ {
		if p[i] < 0x80 || p[i] > 0xbf {
			return i
		}
	}
	return -1
}

func value(b0 byte, rest []byte, need int) rune {
	r := rune(b0 & (0xff >> uint(need)))
	for _, b := range rest {
		r = r<<6 | rune(b&0x3f)
	}
	return r
}
