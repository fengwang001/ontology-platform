// Package scalar 判定单个 Unicode 标量值（Unicode scalar value）。
// 不依赖本模块其他包，也不使用 unicode/utf8、unicode/utf16。
package scalar

// Rune 是一个 Unicode 码点（int32）。
type Rune = rune

const (
	// MaxRune 是 Unicode 最大合法码点 U+10FFFF。
	MaxRune = 0x10FFFF
	// SurrogateMin / SurrogateMax 是代理区 [U+D800,U+DFFF]。
	SurrogateMin = 0xD800
	SurrogateMax = 0xDFFF
	// Replacement 是替换字符 U+FFFD。
	Replacement = 0xFFFD
	// BOM 是字节序标记 U+FEFF。
	BOM = 0xFEFF
)

// IsScalar 报告 r 是否为标量值：未越界且不在代理区。
func IsScalar(r Rune) bool {
	return uint32(r) <= MaxRune && (r < SurrogateMin || r > SurrogateMax)
}

// IsSurrogate 报告 r 是否落在代理区。
func IsSurrogate(r Rune) bool {
	return uint32(r-SurrogateMin) <= SurrogateMax-SurrogateMin
}

// IsHighSurrogate 报告 r 是否为高代理 U+D800..U+DBFF。
func IsHighSurrogate(r Rune) bool {
	return uint32(r-SurrogateMin) <= 0x3FF
}

// IsLowSurrogate 报告 r 是否为低代理 U+DC00..U+DFFF。
func IsLowSurrogate(r Rune) bool {
	return uint32(r-0xDC00) <= 0x3FF
}

// JoinSurrogates 组合一对代理为辅助平面码点，调用方须自行保证两者合法。
func JoinSurrogates(high, low Rune) Rune {
	return 0x10000 + (high-SurrogateMin)<<10 + (low - 0xDC00)
}

// SplitSurrogate 把辅助平面码点拆成高、低代理。
func SplitSurrogate(r Rune) (high, low Rune) {
	r -= 0x10000
	return SurrogateMin + r>>10, 0xDC00 + r&0x3FF
}
