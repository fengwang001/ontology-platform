package scalar

const (
	MaxRune  = 0x10FFFF
	Replaced = '\uFFFD'
	BOM      = '\uFEFF'
)

func IsScalar(r rune) bool { return false }

func IsSurrogate(r rune) bool { return false }
