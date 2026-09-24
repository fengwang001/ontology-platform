package u16

import "ontology/scalar"

type Unit struct {
	R     rune
	Size  int
	Valid bool
}

func DecodeAt(p []byte, little bool) Unit {
	if len(p) < 2 {
		return Unit{Size: 0}
	}
	code := codeAt(p, 0, little)
	if scalar.IsHighSurrogate(code) {
		if len(p) < 4 {
			return Unit{Size: 0}
		}
		next := codeAt(p, 2, little)
		if scalar.IsLowSurrogate(next) {
			return Unit{R: scalar.SurrogatePair(code, next), Size: 4, Valid: true}
		}
		return Unit{Size: 2}
	}
	if scalar.IsLowSurrogate(code) {
		return Unit{Size: 2}
	}
	return Unit{R: code, Size: 2, Valid: true}
}

func Encode(r rune, little bool) []byte {
	if !scalar.IsValid(r) {
		r = scalar.Replacement
	}
	if r < 0x10000 {
		return codeBytes(r, little)
	}
	high, low := scalar.SplitSurrogate(r)
	return append(codeBytes(high, little), codeBytes(low, little)...)
}

func CodeBytes(r rune, little bool) []byte { return codeBytes(r, little) }

func codeAt(p []byte, at int, little bool) rune {
	if little {
		return rune(p[at]) | rune(p[at+1])<<8
	}
	return rune(p[at])<<8 | rune(p[at+1])
}

func codeBytes(r rune, little bool) []byte {
	lo, hi := byte(r), byte(r>>8)
	if little {
		return []byte{lo, hi}
	}
	return []byte{hi, lo}
}
