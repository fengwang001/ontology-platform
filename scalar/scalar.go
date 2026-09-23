// Package scalar 判定单个 Unicode 标量值与编码数值的合法区间。
// 本包不依赖工程内其他包，且不使用 unicode/utf8、unicode/utf16。
package scalar

// Rune 是一个待判定的 Unicode 代码点数值。
type Rune = rune

const (
	// Max 是 Unicode 编码空间的上界。
	Max = 0x10FFFF
	// SurrogateLo 是代理区下界。
	SurrogateLo = 0xD800
	// SurrogateHi 是代理区上界。
	SurrogateHi = 0xDFFF
	// Replacement 是替换字符 U+FFFD。
	Replacement = 0xFFFD
	// BOM 是字节序标记 U+FEFF；位于流中间时它是普通字符。
	BOM = 0xFEFF
)

// IsScalar 报告 r 是否是 Unicode 标量值（未越界且不在代理区）。
func IsScalar(r Rune) bool {
	return uint32(r) <= Max && !(SurrogateLo <= r && r <= SurrogateHi)
}

// IsHighSurrogate 报告 r 是否是高代理代码单元（0xD800..0xDBFF）。
func IsHighSurrogate(r Rune) bool {
	return SurrogateLo <= r && r <= 0xDBFF
}

// IsLowSurrogate 报告 r 是否是低代理代码单元（0xDC00..0xDFFF）。
func IsLowSurrogate(r Rune) bool {
	return 0xDC00 <= r && r <= SurrogateHi
}

// CombineSurrogates 把一对代理还原成标量；仅在高低代理都合法时有意义。
func CombineSurrogates(hi, lo Rune) Rune {
	return 0x10000 + (hi-SurrogateLo)<<10 + (lo - 0xDC00)
}

// SplitSupplementary 把大于 U+FFFF 的标量编码成一对代理；否则返回 r 与 0。
func SplitSupplementary(r Rune) (hi, lo Rune) {
	if r < 0x10000 {
		return r, 0
	}
	v := r - 0x10000
	return SurrogateLo + v>>10, 0xDC00 + v&0x3FF
}
