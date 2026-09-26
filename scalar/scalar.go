// Package scalar 判定单个 Unicode 标量值的合法性。
// 不依赖任何包；所有判定均为字节/数值级，不使用 unicode/utf8 等现成解码。
package scalar

const (
	// Max 是最大的 Unicode 标量值。
	Max = 0x10FFFF
	// Replacement 是非法单元的替换字符 U+FFFD。
	Replacement = 0xFFFD
	// surrogate 区间 [0xD800, 0xDFFF]。
	surrLo = 0xD800
	surrHi = 0xDFFF
)

// Valid 报告 r 是否为合法 Unicode 标量值：
// 在 [0, 0x10FFFF] 内且不在代理区 [0xD800, 0xDFFF]。
func Valid(r int32) bool {
	return r >= 0 && r <= Max && !IsSurrogate(r)
}

// IsSurrogate 报告 r 是否落在代理区。
func IsSurrogate(r int32) bool {
	return r >= surrLo && r <= surrHi
}

// IsHighSurrogate 报告 r 是否为高代理 [0xD800, 0xDBFF]。
func IsHighSurrogate(r int32) bool {
	return r >= surrLo && r <= 0xDBFF
}

// IsLowSurrogate 报告 r 是否为低代理 [0xDC00, 0xDFFF]。
func IsLowSurrogate(r int32) bool {
	return r >= 0xDC00 && r <= surrHi
}
