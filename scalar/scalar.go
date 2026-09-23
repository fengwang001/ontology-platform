package scalar

const (
	MaxRune      = 0x10FFFF
	Replacement  = 0xFFFD
	BOM          = 0xFEFF
	SurrogateMin = 0xD800
	SurrogateMax = 0xDFFF
	HighMin      = 0xD800
	HighMax      = 0xDBFF
	LowMin       = 0xDC00
	LowMax       = 0xDFFF
)

// Valid 报告 r 是否为 Unicode 标量值（非代理、不越界）。
func Valid(r rune) bool {
	return r >= 0 && r <= MaxRune && !IsSurrogate(r)
}

func IsSurrogate(r rune) bool { return r >= SurrogateMin && r <= SurrogateMax }

func IsHighSurrogate(u uint16) bool { return u >= HighMin && u <= HighMax }

func IsLowSurrogate(u uint16) bool { return u >= LowMin && u <= LowMax }

// SurrogatePair 把高/低代理还原成标量值。
func SurrogatePair(high, low uint16) rune {
	return 0x10000 + (rune(high-HighMin) << 10) + rune(low-LowMin)
}
