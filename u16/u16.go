package u16

import "ontology/scalar"

type Order int

const (
	LE Order = iota
	BE
)

type Unit struct {
	Value      scalar.Value
	Bytes      int
	Invalid    bool
	Incomplete bool
}

func word(p []byte, order Order) scalar.Value {
	if order == BE {
		return scalar.Value(uint16(p[0])<<8 | uint16(p[1]))
	}
	return scalar.Value(uint16(p[1])<<8 | uint16(p[0]))
}

func DecodeAt(p []byte, order Order) Unit {
	if len(p) == 0 {
		return Unit{}
	}
	if len(p) == 1 {
		return Unit{Bytes: 1, Incomplete: true}
	}
	w := word(p[:2], order)
	if scalar.HighSurrogate(w) {
		if len(p) < 3 {
			return Unit{Bytes: len(p), Incomplete: true}
		}
		if len(p) < 4 {
			return Unit{Bytes: 3, Incomplete: true}
		}
		n := word(p[2:4], order)
		if scalar.LowSurrogate(n) {
			return Unit{Value: scalar.Pair(w, n), Bytes: 4}
		}
		return Unit{Value: w, Bytes: 2, Invalid: true}
	}
	if scalar.LowSurrogate(w) || w.Surrogate() {
		return Unit{Value: w, Bytes: 2, Invalid: true}
	}
	return Unit{Value: w, Bytes: 2}
}

func Encode(v scalar.Value, order Order) []byte {
	words := []uint16{uint16(v)}
	if rune(v) > 0xFFFF {
		h, l := scalar.Surrogates(v)
		words = []uint16{uint16(h), uint16(l)}
	}
	out := make([]byte, 0, 2*len(words))
	for _, w := range words {
		if order == BE {
			out = append(out, byte(w>>8), byte(w))
		} else {
			out = append(out, byte(w), byte(w>>8))
		}
	}
	return out
}

func Bom(order Order) []byte {
	if order == BE {
		return []byte{0xFE, 0xFF}
	}
	return []byte{0xFF, 0xFE}
}
