package scalar

const (
	Replacement = '\uFFFD'
	MaxRune     = '\U0010FFFF'
	HighMin     = 0xD800
	HighMax     = 0xDBFF
	LowMin      = 0xDC00
	LowMax      = 0xDFFF
)

func Valid(r rune) bool {
	return r >= 0 && r < HighMin || r > LowMax && r <= MaxRune
}

func Surrogate(r rune) bool { return r >= HighMin && r <= LowMax }

func HighSurrogate(v uint16) bool { return v >= HighMin && v <= HighMax }

func LowSurrogate(v uint16) bool { return v >= LowMin && v <= LowMax }

func Continuation(b byte) bool { return b&0xC0 == 0x80 }

func LeadSize(b byte) int {
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
		return 0
	}
}

func SecondOK(lead, second byte) bool {
	switch lead {
	case 0xE0:
		return second >= 0xA0 && second <= 0xBF
	case 0xED:
		return second >= 0x80 && second <= 0x9F
	case 0xF0:
		return second >= 0x90 && second <= 0xBF
	case 0xF4:
		return second >= 0x80 && second <= 0x8F
	default:
		return Continuation(second)
	}
}
