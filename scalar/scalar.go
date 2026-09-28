// Package scalar 判定单个 Unicode 标量值的合法区间。
package scalar

// Max 是 Unicode 标量值上界。
const Max = 0x10FFFF

const (
	surrogateLo = 0xD800
	surrogateHi = 0xDFFF
)

// Replacement 是替换字符 U+FFFD。
const Replacement = 0xFFFD

// IsScalar 报告 r 是否为合法 Unicode 标量值（非代理、不越界）。
func IsScalar(r rune) bool {
	return r >= 0 && r < surrogateLo || r > surrogateHi && r <= Max
}

// IsHighSurrogate 报告 UTF-16 代码单元是否为高代理。
func IsHighSurrogate(u uint16) bool { return u >= surrogateLo && u <= 0xDBFF }

// IsLowSurrogate 报告 UTF-16 代码单元是否为低代理。
func IsLowSurrogate(u uint16) bool { return u >= 0xDC00 && u <= surrogateHi }

// JoinSurrogates 组合一对代理为标量值。
func JoinSurrogates(hi, lo uint16) rune {
	return rune(hi)<<10 + rune(lo) - 0x35FDC00
}

// SplitSurrogates 把 ≥U+10000 的标量拆成高、低代理。
func SplitSurrogates(r rune) (uint16, uint16) {
	v := uint32(r) - 0x10000
	return uint16(v>>10) + surrogateLo, uint16(v&0x3FF) + 0xDC00
}

// NeedsSurrogates 报告编码 UTF-16 时是否需要代理对。
func NeedsSurrogates(r rune) bool { return r >= 0x10000 && r <= Max }

// SecondByteOK 按 UTF-8 首字节 lead 判定候选第 2 字节 b 是否合法。
func SecondByteOK(lead, b byte) bool {
	switch {
	case lead == 0xE0:
		return b >= 0xA0 && b <= 0xBF
	case lead == 0xED:
		return b >= 0x80 && b <= 0x9F
	case lead == 0xF0:
		return b >= 0x90 && b <= 0xBF
	case lead == 0xF4:
		return b >= 0x80 && b <= 0x8F
	case lead >= 0xC2 && lead <= 0xDF:
		fallthrough
	case lead >= 0xE1 && lead <= 0xEC:
		fallthrough
	case lead >= 0xEE && lead <= 0xEF:
		fallthrough
	case lead >= 0xF1 && lead <= 0xF3:
		return b >= 0x80 && b <= 0xBF
	}
	return false
}

// SeqLen 返回合法首字节对应的序列长度，非法首字节返回 0。
func SeqLen(lead byte) int {
	switch {
	case lead >= 0xC2 && lead <= 0xDF:
		return 2
	case lead >= 0xE0 && lead <= 0xEF:
		return 3
	case lead >= 0xF0 && lead <= 0xF4:
		return 4
	}
	return 0
}

// IsCont 报告 b 是否为 10xxxxxx 续字节。
func IsCont(b byte) bool { return b&0xC0 == 0x80 }
