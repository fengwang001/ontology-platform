// Package scalar 提供单个 Unicode 标量值的判定，不依赖其他包。
package scalar

const (
	MaxRune      = 0x10FFFF
	Replacement  = '\uFFFD'
	BOM          = '\uFEFF'
	SurrogateMin = 0xD800
	SurrogateMax = 0xDFFF
	HighMin      = 0xD800
	HighMax      = 0xDBFF
	LowMin       = 0xDC00
	LowMax       = 0xDFFF
)

// IsScalar 报告 r 是否为 Unicode 标量值（非代理、不越界）。
func IsScalar(r rune) bool {
	return r >= 0 && r <= MaxRune && !IsSurrogate(r)
}

// IsSurrogate 报告 r 是否落在 UTF-16 代理区。
func IsSurrogate(r rune) bool { return r >= SurrogateMin && r <= SurrogateMax }

// IsHighSurrogate / IsLowSurrogate 判定 16 位码元。
func IsHighSurrogate(c uint16) bool { return c >= HighMin && c <= HighMax }
func IsLowSurrogate(c uint16) bool { return c >= LowMin && c <= LowMax }

// SurrogatePair 把高/低代理码元组合成标量值。
func SurrogatePair(hi, lo uint16) rune {
	return rune(uint32(hi-HighMin)<<10 | uint32(lo-LowMin) | 0x10000)
}

// SplitSurrogate 把 BMP 之外的标量拆成高/低代理。
func SplitSurrogate(r rune) (uint16, uint16) {
	v := uint32(r) - 0x10000
	return uint16(HighMin + v>>10), uint16(LowMin + v&0x3FF)
}
