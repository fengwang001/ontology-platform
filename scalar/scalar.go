package scalar

const (
	MaxRune     = '\U0010FFFF'
	Replacement = '\uFFFD'
	BOM         = '\uFEFF'
	HighMin     = rune(0xD800)
	HighMax     = rune(0xDBFF)
	LowMin      = rune(0xDC00)
	LowMax      = rune(0xDFFF)
)

func Valid(r rune) bool {
	return r >= 0 && r <= MaxRune && !Surrogate(r)
}

func Surrogate(r rune) bool {
	return r >= HighMin && r <= LowMax
}

func HighSurrogate(r rune) bool {
	return r >= HighMin && r <= HighMax
}

func LowSurrogate(r rune) bool {
	return r >= LowMin && r <= LowMax
}
