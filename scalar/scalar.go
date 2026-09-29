// Package scalar 判定单个 Unicode 标量值（Unicode scalar value）。
// 本包不依赖工程内其他包，也不使用 unicode/utf8、unicode/utf16。
package scalar

// 常量手写，避免引用 unicode 包造成任何隐式解码依赖。
const (
	// Max 是 Unicode 码点空间上界 U+10FFFF。
	Max = 0x10FFFF

	// SurrogateLo / SurrogateHi 是代理区 D800..DFFF。
	SurrogateLo = 0xD800
	SurrogateHi = 0xDFFF

	// Replacement 是替换字符 U+FFFD。
	Replacement = 0xFFFD

	// BOM 是字节序标记 / 零宽不换行符 U+FEFF。
	BOM = 0xFEFF
)

// InRange 报告 r 是否落在 Unicode 码点空间 [0, U+10FFFF] 内。
// 越界（如解码出 U+110000）由此判定，代理区不在此处排除。
func InRange(r rune) bool {
	return r >= 0 && r <= Max
}

// IsSurrogate 报告 r 是否为代理码点 U+D800..U+DFFF。
func IsSurrogate(r rune) bool {
	return r >= SurrogateLo && r <= SurrogateHi
}

// IsHighSurrogate / IsLowSurrogate 区分前后导代理（UTF-16 用）。
func IsHighSurrogate(r rune) bool {
	return r >= 0xD800 && r <= 0xDBFF
}

// IsLowSurrogate 报告 r 是否为低代理 U+DC00..U+DFFF。
func IsLowSurrogate(r rune) bool {
	return r >= 0xDC00 && r <= 0xDFFF
}

// IsScalar 报告 r 是否为合法 Unicode 标量值：
// 在码点空间内，且不属于代理区。
func IsScalar(r rune) bool {
	return InRange(r) && !IsSurrogate(r)
}

// FromSurrogatePair 按 UTF-16 规则把高/低代理还原为码点（不做合法性
// 检查，调用方须先用 IsHighSurrogate/IsLowSurrogate 判定）。
func FromSurrogatePair(high, low rune) rune {
	return 0x10000 + (high-0xD800)<<10 + (low - 0xDC00)
}

// ToSurrogatePair 把 U+10000 以上的码点拆成高/低代理。
func ToSurrogatePair(r rune) (high, low rune) {
	v := r - 0x10000
	return 0xD800 + v>>10, 0xDC00 + v&0x3FF
}
