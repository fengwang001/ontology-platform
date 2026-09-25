// Package scalar 判定单个 Unicode 标量值（Unicode scalar value）。
// 本包不依赖工程内任何其他包。
package scalar

// MaxRune 是 Unicode 标量的上界 U+10FFFF。
const MaxRune = 0x10FFFF

// 替换字符与 BOM。
const (
	Replacement = 0xFFFD
	BOM         = 0xFEFF
)

// UTF-16 代理区边界。
const (
	SurrogateMin = 0xD800
	SurrogateMax = 0xDFFF
	HighMin      = 0xD800
	HighMax      = 0xDBFF
	LowMin       = 0xDC00
	LowMax       = 0xDFFF
)

// Valid 报告 r 是否为合法 Unicode 标量值：0..U+D7FF 与 U+E000..U+10FFFF。
func Valid(r rune) bool {
	return r >= 0 && r < SurrogateMin || r > SurrogateMax && r <= MaxRune
}

// Surrogate 报告 r 是否落在代理区 U+D800..U+DFFF。
func Surrogate(r rune) bool { return r >= SurrogateMin && r <= SurrogateMax }

// HighSurrogate 报告 r 是否为高代理（前导代理）。
func HighSurrogate(r rune) bool { return r >= HighMin && r <= HighMax }

// LowSurrogate 报告 r 是否为低代理（尾随代理）。
func LowSurrogate(r rune) bool { return r >= LowMin && r <= LowMax }

// Pair 把高、低代理组合成标量值。调用方须先自行判定代理类型。
func Pair(high, low rune) rune {
	return 0x10000 + (high-HighMin)<<10 + (low - LowMin)
}

// SplitPair 把 r（必须 > U+FFFF）拆成高、低代理。
func SplitPair(r rune) (high, low rune) {
	v := r - 0x10000
	return HighMin + v>>10, LowMin + v&0x3FF
}
