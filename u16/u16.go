package u16

import "ontology/scalar"

type Order uint8

const (
	LE Order = iota
	BE
)

type Unit struct {
	R       rune
	Size    int
	Invalid bool
}

func Decode(p []byte, order Order) Unit {
	if len(p) == 0 {
		return Unit{}
	}
	if len(p) < 2 {
		return Unit{Size: 1, Invalid: true}
	}
	v := value(p[0], p[1], order)
	if scalar.IsHighSurrogate(v) {
		if len(p) < 4 {
			return Unit{Size: 2, Invalid: true}
		}
		next := value(p[2], p[3], order)
		if scalar.IsLowSurrogate(next) {
			r := rune(v-0xd800)<<10 + rune(next-0xdc00) + 0x10000
			return Unit{R: r, Size: 4}
		}
		return Unit{Size: 2, Invalid: true}
	}
	if scalar.IsLowSurrogate(v) || !scalar.IsScalar(rune(v)) {
		return Unit{Size: 2, Invalid: true}
	}
	return Unit{R: rune(v), Size: 2}
}

func Encode(r rune, p []byte, order Order) int {
	n := EncodedLen(r)
	if len(p) < n {
		return 0
	}
	if n == 2 {
		put(p, uint16(r), order)
		return 2
	}
	v := uint32(r) - 0x10000
	put(p, 0xd800+uint16(v>>10), order)
	put(p[2:], 0xdc00+uint16(v&0x3ff), order)
	return 4
}

func EncodedLen(r rune) int {
	if !scalar.IsScalar(r) {
		r = 0xfffd
	}
	if r >= 0x10000 {
		return 4
	}
	return 2
}

const MaxPending = 3

func value(hi, lo byte, order Order) uint16 {
	if order == BE {
		return uint16(hi)<<8 | uint16(lo)
	}
	return uint16(lo)<<8 | uint16(hi)
}

func put(p []byte, v uint16, order Order) {
	hi, lo := byte(v>>8), byte(v)
	if order == BE {
		p[0], p[1] = hi, lo
	} else {
		p[0], p[1] = lo, hi
	}
}
