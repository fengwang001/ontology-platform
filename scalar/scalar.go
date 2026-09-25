// Package scalar 判定单个 Unicode 标量值（Unicode scalar value）。
// 本包不依赖工程内任何其他包。
package scalar

// MaxRune 是 Unicode 码点空间上界。
const MaxRune = 0x10FFFF

const (
	surrogateMin = 0xD800
	surrogateMax = 0xDFFF
)

// Surrogate 报告码点 r 是否落在 UTF-16 代理区（非标量）。
func Surrogate(r rune) bool {
	return surrogateMin <= r && r <= surrogateMax
}

// HighSurrogate 报告 r 是否为高代理（引导代理）。
func HighSurrogate(r rune) bool {
	return surrogateMin <= r && r <= 0xDBFF
}

// LowSurrogate 报告 r 是否为低代理（尾随代理）。
func LowSurrogate(r rune) bool {
	return 0xDC00 <= r && r <= surrogateMax
}

// Valid 报告 r 是否为合法 Unicode 标量值：
// 非负、不越界、不在代理区。
func Valid(r rune) bool {
	return 0 <= r && r <= MaxRune && !Surrogate(r)
}

// Replacement 是替换字符 U+FFFD。
const Replacement rune = 0xFFFD
