// Package scalar 判断单个 Unicode 标量值。
package scalar

const (
	// Max 是 Unicode 标量值上界。
	Max = 0x10FFFF
	// SurrogateLo..SurrogateHi 是代理区。
	SurrogateLo = 0xD800
	SurrogateHi = 0xDFFF
	// Replacement 是 U+FFFD。
	Replacement = 0xFFFD
	// BOM 是 U+FEFF。
	BOM = 0xFEFF
)

// Valid 报告 r 是否为合法 Unicode 标量值（排除代理区与越界）。
func Valid(r rune) bool {
	return r >= 0 && r <= Max && !(r >= SurrogateLo && r <= SurrogateHi)
}

// IsSurrogate 报告 r 是否落在代理区。
func IsSurrogate(r rune) bool { return r >= SurrogateLo && r <= SurrogateHi }

// IsHighSurrogate 报告 r 是否为高代理（前导代理）。
func IsHighSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDBFF }

// IsLowSurrogate 报告 r 是否为低代理（后尾代理）。
func IsLowSurrogate(r rune) bool { return r >= 0xDC00 && r <= 0xDFFF }

// SurrogatePair 把高、低代理组合成标量值；输入须分别为高、低代理。
func SurrogatePair(hi, lo rune) rune {
	return 0x10000 + (hi-0xD800)<<10 + (lo - 0xDC00)
}

// EncodeSurrogates 返回 r（r>=0x10000）对应的高、低代理。
func EncodeSurrogates(r rune) (hi, lo rune) {
	v := r - 0x10000
	return 0xD800 + v>>10, 0xDC00 + v&0x3FF
}
