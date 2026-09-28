package scalar

const (
	Replacement rune = 0xFFFD
	MaxRune     rune = 0x10FFFF
	HighMin     rune = 0xD800
	LowMax      rune = 0xDFFF
)

func Valid(r rune) bool {
	return r >= 0 && r <= MaxRune && !(r >= HighMin && r <= LowMax)
}

func Surrogate(r rune) bool { return r >= HighMin && r <= LowMax }

func HighSurrogate(r rune) bool { return r >= HighMin && r <= 0xDBFF }

func LowSurrogate(r rune) bool { return r >= 0xDC00 && r <= LowMax }

func DecodeSurrogate(high, low uint16) rune {
	return 0x10000 + (rune(high-HighMin16) << 10) + rune(low-LowMin16)
}

func EncodeSurrogate(r rune) (uint16, uint16) {
	v := uint32(r) - 0x10000
	return HighMin16 + uint16(v>>10), LowMin16 + uint16(v&0x3FF)
}

const (
	HighMin16 uint16 = 0xD800
	LowMin16  uint16 = 0xDC00
)

type Lead struct {
	Len       int
	SecondMin byte
	SecondMax byte
	Invalid   bool
}

func UTF8Lead(b byte) Lead {
	switch {
	case b < 0x80:
		return Lead{Len: 1, SecondMin: 0, SecondMax: 0}
	case b >= 0xC2 && b <= 0xDF:
		return Lead{Len: 2, SecondMin: 0x80, SecondMax: 0xBF}
	case b == 0xE0:
		return Lead{Len: 3, SecondMin: 0xA0, SecondMax: 0xBF}
	case b >= 0xE1 && b <= 0xEC:
		return Lead{Len: 3, SecondMin: 0x80, SecondMax: 0xBF}
	case b == 0xED:
		return Lead{Len: 3, SecondMin: 0x80, SecondMax: 0x9F}
	case b >= 0xEE && b <= 0xEF:
		return Lead{Len: 3, SecondMin: 0x80, SecondMax: 0xBF}
	case b == 0xF0:
		return Lead{Len: 4, SecondMin: 0x90, SecondMax: 0xBF}
	case b >= 0xF1 && b <= 0xF3:
		return Lead{Len: 4, SecondMin: 0x80, SecondMax: 0xBF}
	case b == 0xF4:
		return Lead{Len: 4, SecondMin: 0x80, SecondMax: 0x8F}
	default:
		return Lead{Invalid: true}
	}
}

func Continuation(b byte) bool { return b&0xC0 == 0x80 }
