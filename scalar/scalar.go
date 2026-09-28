package scalar

type Kind uint8

const (
	Ascii Kind = iota + 1
	Two
	Three
	Four
	Continuation
	Invalid
)

func IsScalar(r rune) bool {
	return r >= 0 && r <= 0x10ffff && !(r >= 0xd800 && r <= 0xdfff)
}

func IsSurrogate(r rune) bool { return r >= 0xd800 && r <= 0xdfff }

func IsHighSurrogate(v uint16) bool { return v >= 0xd800 && v <= 0xdbff }

func IsLowSurrogate(v uint16) bool { return v >= 0xdc00 && v <= 0xdfff }

func LeadKind(b byte) Kind {
	switch {
	case b < 0x80:
		return Ascii
	case b >= 0xc2 && b <= 0xdf:
		return Two
	case b >= 0xe0 && b <= 0xef:
		return Three
	case b >= 0xf0 && b <= 0xf4:
		return Four
	case b >= 0x80 && b <= 0xbf:
		return Continuation
	default:
	return Invalid
	}
}

func Need(k Kind) int {
	switch k {
	case Ascii:
		return 1
	case Two:
	return 2
	case Three:
		return 3
	case Four:
		return 4
	default:
		return 0
	}
}

func SecondOK(lead, second byte) bool {
	switch lead {
	case 0xe0:
		return second >= 0xa0 && second <= 0xbf
	case 0xed:
		return second >= 0x80 && second <= 0x9f
	case 0xf0:
		return second >= 0x90 && second <= 0xbf
	case 0xf4:
		return second >= 0x80 && second <= 0x8f
	default:
		return second >= 0x80 && second <= 0xbf
	}
}

func Cont(b byte) bool { return b >= 0x80 && b <= 0xbf }
