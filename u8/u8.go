// Package u8 decodes and encodes UTF-8 at byte level without unicode/utf8.
package u8

import "ontology/scalar"

// Unit is one decode result.
type Unit struct {
	R          rune
	Size       int  // bytes consumed from the input
	Illegal    bool // Size bytes form one illegal unit, R is ignored
	Incomplete bool // Size bytes are a legal prefix; feed more bytes or signal EOF
}

// IsCont reports whether b is a continuation byte.
func IsCont(b byte) bool { return b >= 0x80 && b <= 0xBF }

// LeadLen returns the declared sequence length of a lead byte, or 0.
func LeadLen(b byte) int {
	switch {
	case b >= 0xC2 && b <= 0xDF:
		return 2
	case b >= 0xE0 && b <= 0xEF:
		return 3
	case b >= 0xF0 && b <= 0xF4:
		return 4
	}
	return 0
}

// SecondOK reports whether b2 is a legal second byte after lead b1.
func SecondOK(b1, b2 byte) bool {
	if !IsCont(b2) {
		return false
	}
	switch {
	case b1 == 0xE0:
		return b2 >= 0xA0
	case b1 == 0xED:
		return b2 <= 0x9F
	case b1 == 0xF0:
		return b2 >= 0x90
	case b1 == 0xF4:
		return b2 <= 0x8F
	}
	return true
}

// Decode decodes one unit at the start of buf. An empty buf gives zero Unit.
func Decode(buf []byte) Unit {
	if len(buf) == 0 {
		return Unit{}
	}
	b1 := buf[0]
	if b1 < 0x80 {
		return Unit{R: rune(b1), Size: 1}
	}
	if LeadLen(b1) == 0 { // 80..BF, C0,C1,F5..FF
		return Unit{Size: 1, Illegal: true}
	}
	need := LeadLen(b1)
	if len(buf) < 2 || !SecondOK(b1, buf[1]) {
		if len(buf) < 2 {
			return Unit{Size: len(buf), Incomplete: true}
		}
		return Unit{Size: 1, Illegal: true}
	}
	if need == 2 {
		return Unit{R: rune(b1&0x1F)<<6 | rune(buf[1]&0x3F), Size: 2}
	}
	if len(buf) < 3 || !IsCont(buf[2]) {
		if len(buf) < 3 {
			return Unit{Size: 2, Incomplete: true}
		}
		return Unit{Size: 2, Illegal: true}
}
	if need == 3 {
		return Unit{R: rune(b1&0x0F)<<12 | rune(buf[1]&0x3F)<<6 | rune(buf[2]&0x3F), Size: 3}
	}
	if len(buf) < 4 || !IsCont(buf[3]) {
		if len(buf) < 4 {
			return Unit{Size: 3, Incomplete: true}
		}
		return Unit{Size: 3, Illegal: true}
	}
	return Unit{
		R: rune(b1&0x07)<<18 | rune(buf[1]&0x3F)<<12 | rune(buf[2]&0x3F)<<6 | rune(buf[3]&0x3F),
		Size: 4,
	}
}

// RuneLen returns the UTF-8 byte length of a scalar value.
func RuneLen(r rune) int {
	switch {
	case r < 0x80:
		return 1
	case r < 0x800:
		return 2
	case r < 0x10000:
		return 3
	}
	return 4
}

// Append encodes scalar r onto p. Callers must pass scalar.Valid r.
func Append(p []byte, r rune) []byte {
	if !scalar.Valid(r) {
		panic("u8.Append: invalid scalar")
	}
	switch {
	case r < 0x80:
		return append(p, byte(r))
	case r < 0x800:
		return append(p, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
	case r < 0x10000:
		return append(p, 0xE0|byte(r>>12), 0x80|byte((r>>6)&0x3F), 0x80|byte(r&0x3F))
	}
	return append(p, 0xF0|byte(r>>18), 0x80|byte((r>>12)&0x3F),
		0x80|byte((r>>6)&0x3F), 0x80|byte(r&0x3F))
}
