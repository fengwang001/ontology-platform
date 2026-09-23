// Package scalar 判定单个 Unicode 标量值的合法性，不依赖其他包。
package scalar

// RuneReplacement 是替换字符 U+FFFD。
const RuneReplacement rune = 0xFFFD

// RuneMax 是 Unicode 标量值上界 U+10FFFF。
const RuneMax rune = 0x10FFFF

// IsScalar 报告 r 是否为合法 Unicode 标量值（排除代理区 D800..DFFF 与越界值）。
func IsScalar(r rune) bool {
	return r >= 0 && r <= RuneMax && !IsSurrogate(r)
}

// IsSurrogate 报告 r 是否落在 UTF-16 代理区。
func IsSurrogate(r rune) bool {
	return 0xD800 <= r && r <= 0xDFFF
}

// IsHighSurrogate 报告 r 是否为高代理 D800..DBFF。
func IsHighSurrogate(r rune) bool {
	return 0xD800 <= r && r <= 0xDBFF
}

// IsLowSurrogate 报告 r 是否为低代理 DC00..DFFF。
func IsLowSurrogate(r rune) bool {
	return 0xDC00 <= r && r <= 0xDFFF
}

// SurrogatePair 把高代理 hi 与低代理 lo 组合成标量值。
func SurrogatePair(hi, lo uint16) rune {
	return 0x10000 + (rune(hi)-0xD800)<<10 + (rune(lo) - 0xDC00)
}

// SplitSurrogate 返回标量 r（r>=0x10000）对应的高、低代理码元。
func SplitSurrogate(r rune) (hi, lo uint16) {
	r -= 0x10000
	return uint16(r>>10) + 0xD800, uint16(r&0x3FF) + 0xDC00
}

// IsContByte 报告 b 是否为 UTF-8 续字节 10xxxxxx。
func IsContByte(b byte) bool { return b&0xC0 == 0x80 }

// SecondRangeOK 报告“首字节 first 之后的第二字节 b”是否落在合法区间。
// 它同时排除非最短形式（E0、F0）与代理/越界（ED、F4）。first 须为合法首字节。
func SecondRangeOK(first, b byte) bool {
	switch {
	case first == 0xE0:
		return 0xA0 <= b && b <= 0xBF
	case first == 0xED:
		return 0x80 <= b && b <= 0x9F
	case first == 0xF0:
		return 0x90 <= b && b <= 0xBF
	case first == 0xF4:
		return 0x80 <= b && b <= 0x8F
	default:
		return IsContByte(b)
	}
}

// LeadLen 返回首字节 b 声明的 UTF-8 序列长度；非法首字节返回 0。
func LeadLen(b byte) int {
	switch {
	case 0xC2 <= b && b <= 0xDF:
		return 2
	case 0xE0 <= b && b <= 0xEF:
		return 3
	case 0xF0 <= b && b <= 0xF4:
		return 4
	default:
		return 0
	}
}
