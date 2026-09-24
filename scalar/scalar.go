// Package scalar 判定单个 Unicode 标量值（Unicode scalar value）。
// 不依赖本模块其他包。
package scalar

// Max 是 Unicode 标量值的上界。
const Max = 0x10FFFF

// SurrogateMin..SurrogateMax 是 UTF-16 代理区。
const (
	SurrogateMin = 0xD800
	SurrogateMax = 0xDFFF
)

// IsScalar 报告 r 是否为合法 Unicode 标量值：未越界且不在代理区。
func IsScalar(r rune) bool {
	return uint32(r) <= Max && !IsSurrogate(r)
}

// IsSurrogate 报告 r 是否落在代理区。
func IsSurrogate(r rune) bool {
	return uint32(r)-SurrogateMin <= SurrogateMax-SurrogateMin
}

// IsHighSurrogate 报告 u 是否为高代理 code unit。
func IsHighSurrogate(u uint16) bool { return u >= 0xD800 && u <= 0xDBFF }

// IsLowSurrogate 报告 u 是否为低代理 code unit。
func IsLowSurrogate(u uint16) bool { return u >= 0xDC00 && u <= 0xDFFF }

// FromSurrogatePair 由高、低代理还原增补平面标量值。
// 调用方须自行保证两个代理的合法性。
func FromSurrogatePair(hi, lo uint16) rune {
	return rune(uint32(hi-0xD800)<<10 | uint32(lo-0xDC00) + 0x10000)
}

// ToSurrogatePair 把增补平面标量值编为高、低代理。
func ToSurrogatePair(r rune) (hi, lo uint16) {
	v := uint32(r) - 0x10000
	return uint16(v>>10) + 0xD800, uint16(v&0x3FF) + 0xDC00
}

// Replacement 是替换字符 U+FFFD。
const Replacement rune = 0xFFFD
