package scalar

const (
	Replacement rune = 0xFFFD
	MaxRune     rune = 0x10FFFF
)

func IsScalar(r rune) bool {
	return uint32(r) <= uint32(MaxRune) && !IsSurrogate(r)
}

func IsSurrogate(r rune) bool {
	return uint32(r)-0xD800 < 0x800
}
