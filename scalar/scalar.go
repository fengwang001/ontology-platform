// Package scalar 判断单个 Unicode 标量值的合法性。
package scalar

// RuneMax 是 Unicode 标量值上界。
const RuneMax = 0x10FFFF

const (
	surrogateLo = 0xD800
	surrogateHi = 0xDFFF
)

// IsScalar 判断 r 是否为合法 Unicode 标量值（非代理、不越界、非负）。
func IsScalar(r rune) bool {
	return r >= 0 && r < surrogateLo || r > surrogateHi && r <= RuneMax
}

// IsSurrogate 判断 r 是否落在 UTF-16 代理区。
func IsSurrogate(r rune) bool { return r >= surrogateLo && r <= surrogateHi }

// HighSurrogate 判断 r 是否为高代理（前导代理）。
func HighSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDBFF }

// LowSurrogate 判断 r 是否为低代理（尾随代理）。
func LowSurrogate(r rune) bool { return r >= 0xDC00 && r <= 0xDFFF }

// SurrogatePair 把高、低代理还原成标量值；入参不合法时返回 -1。
func SurrogatePair(hi, lo uint16) rune {
	if !HighSurrogate(rune(hi)) || !LowSurrogate(rune(lo)) {
		return -1
	}
	return 0x10000 + (rune(hi)-0xD800)<<10 + (rune(lo) - 0xDC00)
}

// EncodeSurrogates 把 r（必须 ≥ 0x10000）编成高、低代理。
func EncodeSurrogates(r rune) (hi, lo uint16) {
	r -= 0x10000
	return uint16(0xD800 + r>>10), uint16(0xDC00 + r&0x3FF)
}

// SecondOK 按 UTF-8 首字节 lead 判断第二字节 b 是否在合法区间内。
func SecondOK(lead, b byte) bool {
	if b < 0x80 || b > 0xBF {
		return false
	}
	switch {
	case lead == 0xE0:
		return b >= 0xA0
	case lead == 0xED:
		return b <= 0x9F
	case lead == 0xF0:
		return b >= 0x90
	case lead == 0xF4:
		return b <= 0x8F
	default:
		return true
	}
}

// Cont 判断 b 是否为 UTF-8 延续字节。
func Cont(b byte) bool { return b&0xC0 == 0x80 }
