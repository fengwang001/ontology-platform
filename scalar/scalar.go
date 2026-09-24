package scalar

const (
	MaxUnicode  = 0x10FFFF
	HighMin     = 0xD800
	HighMax     = 0xDBFF
	LowMin      = 0xDC00
	LowMax      = 0xDFFF
	Replacement = '\uFFFD'
)

func Valid(r rune) bool { return r >= 0 && !Surrogate(r) && r <= MaxUnicode }

func Surrogate(r rune) bool { return r >= HighMin && r <= LowMax }

func HighSurrogate(r rune) bool { return r >= HighMin && r <= HighMax }

func LowSurrogate(r rune) bool { return r >= LowMin && r <= LowMax }

func Continuation(b byte) bool { return b&0xC0 == 0x80 }
