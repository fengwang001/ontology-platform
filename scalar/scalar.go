// Package scalar 定义单个 Unicode 标量值的判定：合法区间、代理区、非最短形式、越界。
package scalar

// RuneError 是非法标量替换值 U+FFFD。
const RuneError = 0xFFFD

// IsValid 报告 r 是否为合法 Unicode 标量：[0, 0xD7FF] ∪ [0xE000, 0x10FFFF]。
func IsValid(r rune) bool {
	if r < 0 {
		return false
	}
	if r < 0xD800 {
		return true
	}
	if r <= 0xDFFF {
		return false // 代理区
	}
	return r <= 0x10FFFF
}

// IsSurrogate 报告 r 是否落在 UTF-16 代理区 [0xD800, 0xDFFF]。
func IsSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDFFF }

// IsHighSurrogate 报告 r 是否为高代理 [0xD800, 0xDBFF]。
func IsHighSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDBFF }

// IsLowSurrogate 报告 r 是否为低代理 [0xDC00, 0xDFFF]。
func IsLowSurrogate(r rune) bool { return r >= 0xDC00 && r <= 0xDFFF }

// DecodeU16 把一对代理合并为标量。调用方须先用 IsHighSurrogate/IsLowSurrogate 校验。
func DecodeU16(high, low rune) rune {
	return 0x10000 + ((high-0xD800)<<10 | (low - 0xDC00))
}
