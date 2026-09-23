// Package scalar 判定单个 Unicode 标量值（Unicode scalar value）。
package scalar

// 标量值区间：[0,0xD7FF] 与 [0xE000,0x10FFFF]。
const (
	MaxRune   = 0x10FFFF
	Surrogate = 0xD800
)

const (
	highLo = 0xD800
	highHi = 0xDBFF
	lowLo  = 0xDC00
	lowHi  = 0xDFFF
)

// Valid 报告 r 是否为合法 Unicode 标量值（排除代理区与越界值）。
func Valid(r rune) bool {
	return uint32(r) <= MaxRune && !IsSurrogate(r)
}

// IsSurrogate 报告 r 是否落在代理区 U+D800..U+DFFF。
func IsSurrogate(r rune) bool { return uint32(r)-Surrogate <= 0x7FF }

// IsHighSurrogate 报告 u 是否为 UTF-16 高代理。
func IsHighSurrogate(u uint16) bool { return u >= highLo && u <= highHi }

// IsLowSurrogate 报告 u 是否为 UTF-16 低代理。
func IsLowSurrogate(u uint16) bool { return u >= lowLo && u <= lowHi }

// JoinSurrogate 还原代理对表示的标量值。仅在高/低代理合法时有意义。
func JoinSurrogate(hi, lo uint16) rune {
	return rune(uint32(hi-highLo)<<10 | uint32(lo-lowLo) | 0x10000)
}

// SplitSurrogate 把 U+10000 以上的标量编成高/低代理。
func SplitSurrogate(r rune) (hi, lo uint16) {
	v := uint32(r) - 0x10000
	return uint16(v>>10) + highLo, uint16(v&0x3FF) + lowLo
}
