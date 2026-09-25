package scalar

const (
	Replacement = '\uFFFD'
	MaxRune     = '\U0010FFFF'
	HighMin     = 0xD800
	HighMax     = 0xDBFF
	LowMin      = 0xDC00
	LowMax      = 0xDFFF
)

type Value uint32

func Valid(r Value) bool {
	return r <= 0xD7FF || r >= 0xE000 && r <= MaxRune
}

func Surrogate(r Value) bool {
	return r >= HighMin && r <= LowMax
}

func HighSurrogate(r Value) bool {
	return r >= HighMin && r <= HighMax
}

func LowSurrogate(r Value) bool {
	return r >= LowMin && r <= LowMax
}

func Combine(high, low Value) Value {
	return 0x10000 + (high-HighMin)<<10 + (low - LowMin)
}

func Split(r Value) (high, low Value) {
	r -= 0x10000
	return HighMin + r>>10, LowMin + r&0x3FF
}
