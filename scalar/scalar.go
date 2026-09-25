// Package scalar 判定单个 Unicode 标量值与 UTF 字节类别，不依赖其他包。
package scalar

// Rune 是一个 Unicode 码位（不保证是标量值）。
type Rune = rune

const (
	MaxRune     = 0x10FFFF // Unicode 码位上限
	Surrogate   = 0xD800   // 代理区起点
	SurEnd      = 0xDFFF   // 代理区终点
	Replacement = 0xFFFD   // 替换字符
	BOM         = 0xFEFF   // 字节序标记 / 零宽不换行空格
)

// IsScalar 报告 r 是否为合法 Unicode 标量值（非代理、不越界、非负）。
func IsScalar(r rune) bool {
	return r >= 0 && r <= MaxRune && (r < Surrogate || r > SurEnd)
}

// IsContinuation 报告 b 是否为 UTF-8 续字节 10xxxxxx。
func IsContinuation(b byte) bool { return b&0xC0 == 0x80 }

// LeadLen 返回 UTF-8 首字节声明的序列长度，非法首字节返回 0。
func LeadLen(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b >= 0xC2 && b <= 0xDF:
		return 2
	case b >= 0xE0 && b <= 0xEF:
		return 3
	case b >= 0xF0 && b <= 0xF4:
		return 4
	default:
		return 0
	}
}

// SecondOK 报告在首字节 lead 已收下后，第二字节 c2 是否落在合法区间。
// 区间用于拒绝非最短形式（E0/F0）、代理区（ED）与越界（F4）。
func SecondOK(lead, c2 byte) bool {
	if !IsContinuation(c2) {
		return false
	}
	switch {
	case lead == 0xE0:
		return c2 >= 0xA0
	case lead == 0xED:
		return c2 <= 0x9F
	case lead == 0xF0:
		return c2 >= 0x90
	case lead == 0xF4:
		return c2 <= 0x8F
	default:
		return true
	}
}

// IsHighSurrogate 报告 u 是否为 UTF-16 高代理。
func IsHighSurrogate(u uint16) bool { return u >= 0xD800 && u <= 0xDBFF }

// IsLowSurrogate 报告 u 是否为 UTF-16 低代理。
func IsLowSurrogate(u uint16) bool { return u >= 0xDC00 && u <= 0xDFFF }

// FromSurrogatePair 合并高代理 hi 与低代理 lo 为码位（调用方须先判定）。
func FromSurrogatePair(hi, lo uint16) rune {
	return rune(hi-0xD800)<<10 + rune(lo-0xDC00) + 0x10000
}

// ToSurrogatePair 把 r（≥0x10000）编码为高、低代理。
func ToSurrogatePair(r rune) (hi, lo uint16) {
	r -= 0x10000
	return uint16(r>>10) + 0xD800, uint16(r&0x3FF) + 0xDC00
}
