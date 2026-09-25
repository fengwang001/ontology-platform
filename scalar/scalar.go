// Package scalar 判定单个 Unicode 标量值（Unicode scalar value）。
// 本包不依赖工程内其他包。
package scalar

// Rune 是一个 Unicode 码点。
type Rune = rune

const (
	// MaxRune 是合法码点上界。
	MaxRune = 0x10FFFF
	// SurrogateMin 是代理区下界。
	SurrogateMin = 0xD800
	// SurrogateMax 是代理区上界。
	SurrogateMax = 0xDFFF
	// Replacement 是 U+FFFD 替换字符。
	Replacement = '\uFFFD'
	// BOM 是 U+FEFF。
	BOM = '\uFEFF'
)

// IsScalar 报告 r 是否为 Unicode 标量值：非负、不越界、不在代理区。
func IsScalar(r rune) bool {
	return r >= 0 && r <= MaxRune && (r < SurrogateMin || r > SurrogateMax)
}

// IsHighSurrogate 报告 r 是否为高代理项。
func IsHighSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDBFF }

// IsLowSurrogate 报告 r 是否为低代理项。
func IsLowSurrogate(r rune) bool { return r >= 0xDC00 && r <= 0xDFFF }

// SurrogatePair 把高、低代理项组合成标量值；输入不合法时返回 false。
func SurrogatePair(hi, lo rune) (rune, bool) {
	if !IsHighSurrogate(hi) || !IsLowSurrogate(lo) {
		return 0, false
	}
	return 0x10000 + (hi-0xD800)<<10 + (lo - 0xDC00), true
}

// SplitSurrogate 把 >= 0x10000 的标量拆成高、低代理项；否则返回 false。
func SplitSurrogate(r rune) (hi, lo rune, ok bool) {
	if r < 0x10000 || r > MaxRune {
		return 0, 0, false
	}
	r -= 0x10000
	return 0xD800 + r>>10, 0xDC00 + r&0x3FF, true
}
