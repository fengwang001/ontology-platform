// Package scalar 判定单个 Unicode 标量值的合法性，不依赖其他包。
package scalar

// Rune 是 Unicode 码位的别名，避免直接使用类型转换语义。
type Rune = rune

const (
	// MaxRune 是 Unicode 码位上界 U+10FFFF。
	MaxRune = 0x10FFFF
	// SurrogateMin 是高代理区起点 U+D800。
	SurrogateMin = 0xD800
	// SurrogateMax 是低代理区终点 U+DFFF。
	SurrogateMax = 0xDFFF
	// Replacement 是替换字符 U+FFFD。
	Replacement Rune = 0xFFFD
)

// Valid 报告 r 是否为可编码的 Unicode 标量值：未越界且不在代理区。
func Valid(r Rune) bool {
	return uint32(r) <= MaxRune && !Surrogate(r) && r >= 0
}

// Surrogate 报告 r 是否落在代理区 [U+D800, U+DFFF]。
func Surrogate(r Rune) bool {
	return uint32(r)-SurrogateMin <= SurrogateMax-SurrogateMin
}

// HighSurrogate 报告 r 是否为高代理 [U+D800, U+DBFF]。
func HighSurrogate(r Rune) bool {
	return uint32(r)-SurrogateMin <= 0x3FF
}

// LowSurrogate 报告 r 是否为低代理 [U+DC00, U+DFFF]。
func LowSurrogate(r Rune) bool {
	return uint32(r)-0xDC00 <= 0x3FF
}

// CombineSurrogates 按标准公式合并代理对，调用方须自行保证二者类别合法。
func CombineSurrogates(hi, lo Rune) Rune {
	return 0x10000 + (hi-SurrogateMin)<<10 + (lo - 0xDC00)
}
