// Package scalar 判定单个 Unicode 标量值。本包不依赖工程内其他包，
// 也不使用 unicode/utf8、unicode/utf16 或任何隐式解码。
package scalar

// Max 是 Unicode 码点上界。
const Max = 0x10FFFF

// RFFFD 是替换字符 U+FFFD。
const RFFFD rune = 0xFFFD

// BOM 是字节序标记 U+FEFF。
const BOM rune = 0xFEFF

// HighSurrogateStart/End 与 LowSurrogateStart/End 划定 UTF-16 代理区。
const (
	HighSurrogateStart = 0xD800
	HighSurrogateEnd   = 0xDBFF
	LowSurrogateStart  = 0xDC00
	LowSurrogateEnd    = 0xDFFF
)

// Surrogate 报告 r 是否落在代理区 [D800,DFFF]。
func Surrogate(r rune) bool {
	return r >= HighSurrogateStart && r <= LowSurrogateEnd
}

// Valid 报告 r 是否是合法 Unicode 标量值：未越界且不是代理码点。
func Valid(r rune) bool {
	return r >= 0 && r <= Max && !Surrogate(r)
}

// IsHighSurrogate 与 IsLowSurrogate 判定单个 UTF-16 code unit 的代理角色。
func IsHighSurrogate(u uint16) bool {
	return u >= HighSurrogateStart && u <= HighSurrogateEnd
}

// IsLowSurrogate 判定低代理。
func IsLowSurrogate(u uint16) bool {
	return u >= LowSurrogateStart && u <= LowSurrogateEnd
}

// CombineSurrogates 按标准公式把高、低代理还原成码点（调用方需自行保证代理合法）。
func CombineSurrogates(hi, lo uint16) rune {
	return 0x10000 + (rune(hi-HighSurrogateStart) << 10) + rune(lo-LowSurrogateStart)
}

// SplitSurrogate 把 r（必须 >= 0x10000）拆成高、低代理。
func SplitSurrogate(r rune) (uint16, uint16) {
	v := uint32(r) - 0x10000
	return uint16(HighSurrogateStart + v>>10), uint16(LowSurrogateStart + v&0x3FF)
}
