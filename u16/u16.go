package u16

import "ontology/scalar"

type Order int

const (
	LittleEndian Order = iota
	BigEndian
)

type Unit struct {
	R          rune
	Size       int
	Bad        int
	Want       int
	Valid      bool
	Incomplete bool
}

func value(p []byte, order Order) uint16 {
	if order == BigEndian {
		return uint16(p[0])<<8 | uint16(p[1])
	}
	return uint16(p[1])<<8 | uint16(p[0])
}

func put(v uint16, order Order) []byte {
	if order == BigEndian {
		return []byte{byte(v >> 8), byte(v)}
	}
	return []byte{byte(v), byte(v >> 8)}
}

func Decode(p []byte, order Order) Unit {
	if len(p) == 0 {
		return Unit{}
	}
	if len(p) == 1 {
		return Unit{Size: 1, Want: 2, Incomplete: true}
	}
	v := value(p[:2], order)
	if scalar.HighSurrogate(v) {
		if len(p) < 4 {
			return Unit{Size: 2, Want: 4, Incomplete: true}
		}
		w := value(p[2:4], order)
		if !scalar.LowSurrogate(w) {
			return Unit{Size: 2, Bad: 2}
		}
		r := rune(v-0xD800)<<10 | rune(w-0xDC00)
		return Unit{R: r + 0x10000, Size: 4, Valid: true}
	}
	if scalar.Surrogate(rune(v)) {
		return Unit{Size: 2, Bad: 2}
	}
	return Unit{R: rune(v), Size: 2, Valid: true}
}

func Encode(r rune, order Order) []byte {
	if !scalar.Valid(r) {
		r = scalar.Replacement
	}
	if r < 0x10000 {
		return put(uint16(r), order)
	}
	r -= 0x10000
	hi := uint16(0xD800 + r>>10)
	lo := uint16(0xDC00 + r&0x3FF)
	return append(put(hi, order), put(lo, order)...)
}
