// Package scalar 判定单个 Unicode 标量值与 UTF-16 代理码元。
// 本包不依赖工程内其他包。
package scalar

const (
	// MaxRune 是 Unicode 最大码点。
	MaxRune = 0x10FFFF
	// Replacement 是替换字符 U+FFFD。
	Replacement = '\uFFFD'
	surrHigh    = 0xD800
	surrLow     = 0xDC00
	surrEnd     = 0xE000
)

// Valid 报告 r 是否为 Unicode 标量值（非代理且不越界）。
func Valid(r rune) bool {
	return uint32(r) < surrHigh || (uint32(r) >= surrEnd && uint32(r) <= MaxRune)
}

// Surrogate 报告 r 是否落在代理区。
func Surrogate(r rune) bool {
	return uint32(r) >= surrHigh && uint32(r) < surrEnd
}

// HighSurrogate 报告码元 u 是否为高代理。
func HighSurrogate(u uint16) bool {
	return u >= surrHigh && u < surrLow
}

// LowSurrogate 报告码元 u 是否为低代理。
func LowSurrogate(u uint16) bool {
	return u >= surrLow && u < surrEnd
}

// SurrogatePair 把高、低代理组合成标量值。
func SurrogatePair(hi, lo uint16) rune {
	return 0x10000 + rune(hi-surrHigh)<<10 + rune(lo-surrLow)
}

// SplitSurrogate 返回 r（r ≥ 0x10000）对应的高、低代理。
func SplitSurrogate(r rune) (uint16, uint16) {
	r -= 0x10000
	return surrHigh + uint16(r>>10), surrLow + uint16(r&0x3FF)
}
