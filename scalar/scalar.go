// Package scalar 提供单个 Unicode 标量值的判定与 UTF-16 代理对算术。
// 本包不依赖工程内其他包，也不使用 unicode/utf8、unicode/utf16。
package scalar

const (
	// Max 是 Unicode 标量值上界。
	Max = 0x10FFFF
	// Replacement 是 U+FFFD 替换字符。
	Replacement rune = 0xFFFD

	surrLo = 0xD800
	surrHi = 0xDFFF
	highLo = 0xD800
	highHi = 0xDBFF
	lowLo  = 0xDC00
	lowHi  = 0xDFFF
)

// Valid 报告 r 是否为合法 Unicode 标量值（非负、不越界、非代理区）。
func Valid(r rune) bool {
	return r >= 0 && r <= Max && !Surrogate(r)
}

// Surrogate 报告 r 是否落在代理区 U+D800..U+DFFF。
func Surrogate(r rune) bool { return r >= surrLo && r <= surrHi }

// InRange 报告 r 是否在 [lo,hi] 闭区间内（用于越界判定）。
func InRange(r, lo, hi rune) bool { return r >= lo && r <= hi }

// IsHighSurrogate 报告编码单元是否为高代理。
func IsHighSurrogate(u uint16) bool { return u >= highLo && u <= highHi }

// IsLowSurrogate 报告编码单元是否为低代理。
func IsLowSurrogate(u uint16) bool { return u >= lowLo && u <= lowHi }

// SurrogatePair 把 r（必须 > 0xFFFF 且合法）编码为高、低代理。
func SurrogatePair(r rune) (hi, lo uint16) {
	v := uint32(r) - 0x10000
	return highLo + uint16(v>>10), lowLo + uint16(v&0x3FF)
}

// FromSurrogates 还原代理对；任一单元类型不符时 ok 为 false。
func FromSurrogates(hi, lo uint16) (r rune, ok bool) {
	if !IsHighSurrogate(hi) || !IsLowSurrogate(lo) {
		return 0, false
	}
	return 0x10000 + (rune(hi-highLo) << 10) + rune(lo-lowLo), true
}

// Cont 报告 b 是否为 UTF-8 延续字节 10xxxxxx。
func Cont(b byte) bool { return b&0xC0 == 0x80 }
