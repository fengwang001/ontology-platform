// Package scalar 判定单个 Unicode 标量值，不依赖本工程其他包。
package scalar

// 边界常量。
const (
	MaxRune         = 0x10FFFF
	SurrogateMin    = 0xD800
	SurrogateMax    = 0xDFFF
	ReplacementRune = '\uFFFD'
)

// IsSurrogate 判断 r 是否落在 UTF-16 代理区。
func IsSurrogate(r rune) bool {
	return r >= SurrogateMin && r <= SurrogateMax
}

// IsScalar 判断 r 是否是合法 Unicode 标量值（含非字符；排除越界与代理区）。
func IsScalar(r rune) bool {
	return r >= 0 && r <= MaxRune && !IsSurrogate(r)
}

// IsHighSurrogate / IsLowSurrogate 判定 UTF-16 代理类别。
func IsHighSurrogate(c uint16) bool { return c >= 0xD800 && c <= 0xDBFF }

// IsLowSurrogate 判定低代理。
func IsLowSurrogate(c uint16) bool { return c >= 0xDC00 && c <= 0xDFFF }

// DecodeSurrogatePair 把高、低代理组合成标量；输入不合法时返回 false。
func DecodeSurrogatePair(hi, lo uint16) (rune, bool) {
	if !IsHighSurrogate(hi) || !IsLowSurrogate(lo) {
		return 0, false
	}
	return rune(hi)<<10 + rune(lo) - 0x35FDC00 + 0x10000, true
}

// EncodeSurrogatePair 把 r（必须 ≥0x10000）编成高、低代理。
func EncodeSurrogatePair(r rune) (uint16, uint16) {
	v := uint32(r) - 0x10000
	return uint16(v>>10) + 0xD800, uint16(v&0x3FF) + 0xDC00
}
