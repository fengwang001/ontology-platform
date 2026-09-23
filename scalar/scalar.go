package scalar

const (
	Max           uint32 = 0x10FFFF
	Replacement   uint32 = 0xFFFD
	ByteOrderMark uint32 = 0xFEFF
	SurrogateLo   uint32 = 0xD800
	SurrogateHi   uint32 = 0xDFFF
)

func Valid(r uint32) bool {
	return r < SurrogateLo || (r > SurrogateHi && r <= Max)
}

func Surrogate(r uint32) bool {
	return r >= SurrogateLo && r <= SurrogateHi
}

func Overlong(byteLen int, r uint32) bool {
	switch byteLen {
	case 2:
		return r < 0x80
	case 3:
		return r < 0x800
	case 4:
		return r < 0x10000
	default:
		return false
	}
}

func OutOfRange(r uint32) bool {
	return r > Max
}
