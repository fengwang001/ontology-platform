package u16

import "ontology/scalar"

type Unit struct {
	R       rune
	Size    int
	Next    int
	Valid   bool
	Partial bool
}

func codeUnit(p []byte, little bool) uint16 {
	if little {
		return uint16(p[0]) | uint16(p[1])<<8
	}
	return uint16(p[0])<<8 | uint16(p[1])
}

func DecodeAt(p []byte, little bool) Unit {
	if len(p) == 0 {
		return Unit{}
	}
	if len(p) == 1 {
		return Unit{R: scalar.Replacement, Size: 1, Partial: true}
	}
	v := codeUnit(p[:2], little)
	if scalar.HighSurrogate(v) {
		if len(p) == 2 {
			return Unit{R: scalar.Replacement, Size: 2, Partial: true}
		}
		w := codeUnit(p[2:4], little)
		if scalar.LowSurrogate(w) {
			r := rune(v-scalar.HighMin)<<10 | rune(w-scalar.LowMin)
			return Unit{R: r + 0x10000, Size: 4, Next: 4, Valid: true}
		}
		return Unit{R: scalar.Replacement, Size: 2, Next: 2}
	}
	return Unit{R: rune(v), Size: 2, Next: 2, Valid: !scalar.Surrogate(rune(v))}
}

func Append(dst []byte, r rune, little bool) []byte {
	put := func(v uint16) {
		if little {
			dst = append(dst, byte(v), byte(v>>8))
		} else {
			dst = append(dst, byte(v>>8), byte(v))
		}
	}
	if r >= 0x10000 {
		v := r - 0x10000
		put(uint16(scalar.HighMin + v>>10))
		put(uint16(scalar.LowMin + v&0x3ff))
	} else {
		put(uint16(r))
	}
	return dst
}

func EncodedLen(r rune) int {
	if r >= 0x10000 {
		return 4
	}
	return 2
}
