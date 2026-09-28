package u8

import "ontology/scalar"

const MaxPending = 3

type Unit struct {
	R       rune
	Size    int
	Invalid bool
}

func Decode(p []byte) Unit {
	if len(p) == 0 {
		return Unit{}
	}
	lead := p[0]
	k := scalar.LeadKind(lead)
	need := scalar.Need(k)
	if need == 0 {
		return Unit{Size: 1, Invalid: true}
	}
	if len(p) < need {
		return Unit{Size: len(p), Invalid: true}
	}
	for i := 1; i < need; i++ {
		if !scalar.Cont(p[i]) || (i == 1 && !scalar.SecondOK(lead, p[1])) {
			return Unit{Size: i, Invalid: true}
		}
	}
	var r rune
	switch need {
	case 1:
		r = rune(lead)
	case 2:
		r = rune(lead&0x1f)<<6 | rune(p[1]&0x3f)
	case 3:
		r = rune(lead&0x0f)<<12 | rune(p[1]&0x3f)<<6 | rune(p[2]&0x3f)
	case 4:
		r = rune(lead&0x07)<<18 | rune(p[1]&0x3f)<<12 | rune(p[2]&0x3f)<<6 | rune(p[3]&0x3f)
	}
	if !scalar.IsScalar(r) {
		return Unit{Size: need, Invalid: true}
	}
	return Unit{R: r, Size: need}
}

func Encode(r rune, p []byte) int {
	n := EncodedLen(r)
	if len(p) < n {
		return 0
	}
	switch n {
	case 1:
		p[0] = byte(r)
	case 2:
		p[0] = 0xc0 | byte(r>>6)
		p[1] = 0x80 | byte(r&0x3f)
	case 3:
		p[0] = 0xe0 | byte(r>>12)
		p[1] = 0x80 | byte((r>>6)&0x3f)
		p[2] = 0x80 | byte(r&0x3f)
	case 4:
		p[0] = 0xf0 | byte(r>>18)
		p[1] = 0x80 | byte((r>>12)&0x3f)
		p[2] = 0x80 | byte((r>>6)&0x3f)
		p[3] = 0x80 | byte(r&0x3f)
	}
	return n
}

func EncodedLen(r rune) int {
	switch {
	case !scalar.IsScalar(r):
		return 3
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

func PendingLead(b byte) bool {
	k := scalar.LeadKind(b)
	return k == scalar.Two || k == scalar.Three || k == scalar.Four
}

func Need(b byte) int { return scalar.Need(scalar.LeadKind(b)) }
