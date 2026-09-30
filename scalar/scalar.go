package scalar

const (
	MaxRune       = 0x10FFFF
	Replacement   = 0xFFFD
	BOM           = 0xFEFF
	SurrogateMin  = 0xD800
	SurrogateMax  = 0xDFFF
	HighSurrogate = 0xD800
	LowSurrogate  = 0xDC00
)

func Valid(r rune) bool { return false }

func IsSurrogate(r rune) bool { return false }

func IsHigh(u uint16) bool { return false }

func IsLow(u uint16) bool { return false }

func Pair(hi, lo uint16) (rune, bool) { return 0, false }
