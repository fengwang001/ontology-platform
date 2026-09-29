// Package scalar 判定单个 Unicode 标量值：合法区间、代理区、越界。
package scalar

const (
	// Replacement 是 U+FFFD。
	Replacement rune = 0xFFFD
	// BOM 是 U+FEFF。
	BOM rune = 0xFEFF
	maxRune = 0x10FFFF
	highLo   = 0xD800
	highHi   = 0xDBFF
	lowLo    = 0xDC00
	lowHi    = 0xDFFF
)

// Valid 报告 r 是否为合法 Unicode 标量值。
func Valid(r rune) bool {
	return r >= 0 && r < highLo || r > lowHi && r <= maxRune
}

// IsSurrogate 报告 r 是否落在代理区（高代理或低代理）。
func IsSurrogate(r rune) bool {
	return uint32(r)-highLo <= lowHi-highLo
}

// IsHighSurrogate 报告 r 是否为高代理代码单元。
func IsHighSurrogate(r rune) bool {
	return r >= highLo && r <= highHi
}

// IsLowSurrogate 报告 r 是否为低代理代码单元。
func IsLowSurrogate(r rune) bool {
	return r >= lowLo && r <= lowHi
}

// Pair 把高、低代理组合成标量值；入参非法时返回 false。
func Pair(high, low rune) (rune, bool) {
	if !IsHighSurrogate(high) || !IsLowSurrogate(low) {
		return Replacement, false
	}
	return 0x10000 + (high-highLo)<<10 + (low - lowLo), true
}

// InRange 报告 r 是否落在闭区间 [lo, hi]。
func InRange(r, lo, hi rune) bool {
	return r >= lo && r <= hi
}
